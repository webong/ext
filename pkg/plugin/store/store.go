// Package store installs, upgrades, lists and removes reviewed packages under
// a caller-supplied root. A package is a packagekit manifest plus the artifacts
// it lists. The store has no default location and no product vocabulary; it
// copies and re-verifies content but never launches it or grants authority.
// Concurrent mutation from several processes is the caller's responsibility.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/packagekit"
)

// ManifestName is the manifest file at the top of every package directory. An
// artifact may not use this path.
const ManifestName = "package.json"

// DefaultMaxArtifactBytes bounds one artifact when Limits does not.
const DefaultMaxArtifactBytes int64 = 4 << 30

const maxManifestBytes = 16 << 20

var (
	ErrExists   = errors.New("plugin package already installed")
	ErrNotFound = errors.New("plugin package not installed")
)

// Limits are optional size bounds. Zero values use defaults.
type Limits struct {
	MaxArtifactBytes int64
}

type Store struct {
	root   string
	limits Limits
	mu     sync.Mutex
}

// New returns a store rooted at root. The directory is created on first
// install. root must be supplied by the consumer.
func New(root string, limits Limits) (*Store, error) {
	if root == "" || limits.MaxArtifactBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	if limits.MaxArtifactBytes == 0 {
		limits.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	return &Store{root: root, limits: limits}, nil
}

func (s *Store) Root() string { return s.root }

// Installed is one package directory. Methods re-verify content on use.
type Installed struct {
	Manifest  packagekit.Manifest
	Directory string
	limit     int64
}

// dirName maps a package ID to one directory name. IDs permit "/" but not "%",
// so the encoding is unambiguous.
func dirName(id string) string { return strings.ReplaceAll(id, "/", "%2f") }

func (s *Store) dir(id string) string { return filepath.Join(s.root, dirName(id)) }

func validID(id string) bool {
	return (plugin.Identity{ID: id, Revision: "r"}).Validate() == nil
}

// Load reads and verifies one installed package.
func (s *Store) Load(id string) (Installed, error) {
	if !validID(id) {
		return Installed{}, plugin.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(id)
}

func (s *Store) load(id string) (Installed, error) {
	dir := s.dir(id)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Installed{}, ErrNotFound
	}
	if err != nil {
		return Installed{}, err
	}
	if !info.IsDir() {
		return Installed{}, fmt.Errorf("%w: package directory is not a directory", plugin.ErrInvalid)
	}
	m, err := readManifest(dir)
	if err != nil {
		return Installed{}, err
	}
	if m.Descriptor.Identity.ID != id {
		return Installed{}, fmt.Errorf("%w: directory does not match manifest", plugin.ErrMismatch)
	}
	p := Installed{Manifest: m, Directory: dir, limit: s.limits.MaxArtifactBytes}
	if err := p.Verify(); err != nil {
		return Installed{}, err
	}
	return p, nil
}

// List verifies every installed package and fails closed on the first bad one.
// A missing root is an empty store. Staging directories (leading ".") are skipped.
func (s *Store) List() ([]Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Installed
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			return nil, fmt.Errorf("%w: unexpected entry %q", plugin.ErrInvalid, e.Name())
		}
		id := strings.ReplaceAll(e.Name(), "%2f", "/")
		if !validID(id) {
			return nil, fmt.Errorf("%w: invalid package directory %q", plugin.ErrInvalid, e.Name())
		}
		p, err := s.load(id)
		if err != nil {
			return nil, fmt.Errorf("package %s: %w", id, err)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest.Descriptor.Identity.ID < out[j].Manifest.Descriptor.Identity.ID
	})
	return out, nil
}

// Install copies the package in source (a directory holding ManifestName and
// the listed artifacts) into the store. It never replaces an existing package.
func (s *Store) Install(source string) (Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.checkSource(source)
	if err != nil {
		return Installed{}, err
	}
	id := m.Descriptor.Identity.ID
	dest := s.dir(id)
	if _, err := os.Lstat(dest); err == nil {
		return Installed{}, fmt.Errorf("%w: %s", ErrExists, id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Installed{}, err
	}
	return s.stage(source, m, dest)
}

// Upgrade replaces an installed package with a new revision of the same ID.
// Contract names and versions must match; operations may change, so the caller
// must re-review authority. The previous installation survives any failure.
func (s *Store) Upgrade(source string) (Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.checkSource(source)
	if err != nil {
		return Installed{}, err
	}
	id := m.Descriptor.Identity.ID
	dest := s.dir(id)
	old, err := readManifest(dest)
	if errors.Is(err, os.ErrNotExist) {
		return Installed{}, ErrNotFound
	}
	if err != nil {
		return Installed{}, err
	}
	if old.Descriptor.Identity.ID != id || !sameContracts(old.Descriptor, m.Descriptor) {
		return Installed{}, fmt.Errorf("%w: upgrade must keep ID and contracts", plugin.ErrMismatch)
	}
	if old.Descriptor.Identity.Revision == m.Descriptor.Identity.Revision {
		return Installed{}, fmt.Errorf("%w: upgrade requires a new revision", plugin.ErrInvalid)
	}
	backupRoot, err := s.tempDir(".backup-")
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(backupRoot)
	staged, err := s.stage(source, m, filepath.Join(backupRoot, "new"))
	if err != nil {
		return Installed{}, err
	}
	backup := filepath.Join(backupRoot, "old")
	if err := os.Rename(dest, backup); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(staged.Directory, dest); err != nil {
		if restore := os.Rename(backup, dest); restore != nil {
			return Installed{}, errors.Join(err, fmt.Errorf("previous package retained at %s: %w", backup, restore))
		}
		return Installed{}, err
	}
	staged.Directory = dest
	return staged, nil
}

// Remove deletes one installed package directory. It never follows links.
func (s *Store) Remove(id string) error {
	if !validID(id) {
		return plugin.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.dir(id)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: package directory is not a directory", plugin.ErrInvalid)
	}
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	if m.Descriptor.Identity.ID != id {
		return fmt.Errorf("%w: directory does not match manifest", plugin.ErrMismatch)
	}
	return os.RemoveAll(dir)
}

func (s *Store) checkSource(source string) (packagekit.Manifest, error) {
	m, err := readManifest(source)
	if err != nil {
		return m, err
	}
	for _, a := range m.Artifacts {
		if a.Path == ManifestName {
			return m, fmt.Errorf("%w: artifact path %q is reserved", plugin.ErrInvalid, ManifestName)
		}
	}
	if err := (Installed{Manifest: m, Directory: source, limit: s.limits.MaxArtifactBytes}).Verify(); err != nil {
		return m, err
	}
	return m, nil
}

func (s *Store) tempDir(prefix string) (string, error) {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(s.root, prefix)
}

// stage copies into a fresh directory, verifies it, then renames it to dest.
func (s *Store) stage(source string, m packagekit.Manifest, dest string) (Installed, error) {
	staging, err := s.tempDir(".install-")
	if err != nil {
		return Installed{}, err
	}
	defer os.RemoveAll(staging)
	executable := map[string]bool{}
	for _, e := range m.Entrypoints {
		executable[e.Artifact] = true
	}
	for _, a := range m.Artifacts {
		mode := os.FileMode(0o600)
		if executable[a.Name] {
			mode = 0o700
		}
		if err := copyArtifact(source, staging, a, s.limits.MaxArtifactBytes, mode); err != nil {
			return Installed{}, fmt.Errorf("artifact %s: %w", a.Name, err)
		}
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Installed{}, err
	}
	if err := os.WriteFile(filepath.Join(staging, ManifestName), append(encoded, '\n'), 0o600); err != nil {
		return Installed{}, err
	}
	p := Installed{Manifest: m, Directory: staging, limit: s.limits.MaxArtifactBytes}
	if err := p.Verify(); err != nil {
		return Installed{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return Installed{}, err
	}
	if err := os.Rename(staging, dest); err != nil {
		return Installed{}, err
	}
	p.Directory = dest
	return p, nil
}

func copyArtifact(srcRoot, dstRoot string, a packagekit.Artifact, max int64, mode os.FileMode) error {
	src, err := securePath(srcRoot, a.Path)
	if err != nil {
		return err
	}
	dst := filepath.Join(dstRoot, filepath.FromSlash(a.Path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(out, io.TeeReader(io.LimitReader(in, max+1), h))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if n > max {
		return fmt.Errorf("%w: exceeds size limit", plugin.ErrInvalid)
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("%w: changed during install", plugin.ErrMismatch)
	}
	return nil
}

func readManifest(dir string) (packagekit.Manifest, error) {
	path, err := securePath(dir, ManifestName)
	if err != nil {
		return packagekit.Manifest{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return packagekit.Manifest{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return packagekit.Manifest{}, err
	}
	if len(data) > maxManifestBytes {
		return packagekit.Manifest{}, fmt.Errorf("%w: manifest exceeds 16 MiB", plugin.ErrInvalid)
	}
	return packagekit.Decode(data)
}

func sameContracts(a, b plugin.Descriptor) bool {
	if len(a.Contracts) != len(b.Contracts) {
		return false
	}
	seen := map[plugin.ContractRef]int{}
	for _, c := range a.Contracts {
		seen[c.ContractRef]++
	}
	for _, c := range b.Contracts {
		seen[c.ContractRef]--
		if seen[c.ContractRef] < 0 {
			return false
		}
	}
	return true
}

// securePath resolves a slash path under root, refusing symbolic links and
// non-regular final entries.
func securePath(root, name string) (string, error) {
	current := root
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", plugin.ErrInvalid
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: symbolic links are not allowed", plugin.ErrInvalid)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", plugin.ErrInvalid
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("%w: payload must be a regular file", plugin.ErrInvalid)
		}
	}
	return current, nil
}

// Verify rechecks the manifest, every listed artifact (type, size, digest) and
// that the directory holds nothing else, so Digest is stable and complete.
func (p Installed) Verify() error {
	if err := p.Manifest.Validate(); err != nil {
		return err
	}
	max := p.limit
	if max == 0 {
		max = DefaultMaxArtifactBytes
	}
	listed := map[string]bool{ManifestName: true}
	for _, a := range p.Manifest.Artifacts {
		path, err := securePath(p.Directory, a.Path)
		if err != nil {
			return fmt.Errorf("artifact %s: %w", a.Name, err)
		}
		if err := checkDigest(path, a.SHA256, max); err != nil {
			return fmt.Errorf("artifact %s: %w", a.Name, err)
		}
		listed[strings.ToLower(a.Path)] = true
	}
	if _, err := securePath(p.Directory, ManifestName); err != nil {
		return err
	}
	return filepathWalkExtra(p.Directory, listed)
}

func filepathWalkExtra(root string, listed map[string]bool) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !listed[strings.ToLower(filepath.ToSlash(rel))] {
			return fmt.Errorf("%w: unlisted file %q", plugin.ErrInvalid, filepath.ToSlash(rel))
		}
		return nil
	})
}

func checkDigest(path, digest string, max int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, max+1))
	if err != nil {
		return err
	}
	if n > max {
		return fmt.Errorf("%w: exceeds size limit", plugin.ErrInvalid)
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return plugin.ErrMismatch
	}
	return nil
}

// Digest returns plugin.DirectoryDigest of the verified package directory, for
// consumers that record trust against exact installed content.
func (p Installed) Digest() (string, error) {
	if err := p.Verify(); err != nil {
		return "", err
	}
	return plugin.DirectoryDigest(p.Directory)
}

// ArtifactPath verifies the named artifact and returns its filesystem path
// (under Directory). Use it to locate source files or an executable.
func (p Installed) ArtifactPath(name string) (string, error) {
	for _, a := range p.Manifest.Artifacts {
		if a.Name == name {
			path, err := securePath(p.Directory, a.Path)
			if err != nil {
				return "", err
			}
			max := p.limit
			if max == 0 {
				max = DefaultMaxArtifactBytes
			}
			if err := checkDigest(path, a.SHA256, max); err != nil {
				return "", fmt.Errorf("artifact %s: %w", name, err)
			}
			return path, nil
		}
	}
	return "", plugin.ErrNotFound
}
