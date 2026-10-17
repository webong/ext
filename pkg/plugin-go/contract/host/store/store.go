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
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/package"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

// Defaults for Limits.
const (
	// DefaultMaxTreeFiles bounds the files in one tree artifact.
	DefaultMaxTreeFiles = 250000
	// DefaultMaxTreeBytes bounds the total size of one tree artifact.
	DefaultMaxTreeBytes int64 = 16 << 30
)

// Limits are optional size bounds. Zero values use defaults.
type Limits struct {
	// MaxArtifactBytes bounds one file, including each file inside a tree.
	MaxArtifactBytes int64
	// MaxTreeFiles bounds the files in one tree artifact.
	MaxTreeFiles int
	// MaxTreeBytes bounds the total bytes of one tree artifact.
	MaxTreeBytes int64
}

func (l Limits) normalize() Limits {
	if l.MaxArtifactBytes == 0 {
		l.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	if l.MaxTreeFiles == 0 {
		l.MaxTreeFiles = DefaultMaxTreeFiles
	}
	if l.MaxTreeBytes == 0 {
		l.MaxTreeBytes = DefaultMaxTreeBytes
	}
	return l
}

type Store struct {
	root   string
	limits Limits
	mu     sync.Mutex
}

// New returns a store rooted at root. The directory is created on first
// install. root must be supplied by the consumer.
func New(root string, limits Limits) (*Store, error) {
	if root == "" || limits.MaxArtifactBytes < 0 || limits.MaxTreeFiles < 0 || limits.MaxTreeBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	return &Store{root: root, limits: limits.normalize()}, nil
}

func (s *Store) Root() string { return s.root }

// Installed is one package directory. Methods re-verify content on use.
type Installed struct {
	Manifest  packagekit.Manifest
	Directory string
	limits    Limits
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
	p, err := s.loadManifest(id)
	if err != nil {
		return Installed{}, err
	}
	if err := p.Verify(); err != nil {
		return Installed{}, err
	}
	return p, nil
}

// loadManifest reads and validates one package's manifest and checks that the
// directory matches it. It does not read, hash or walk any content: the result
// is unverified. The caller holds s.mu.
func (s *Store) loadManifest(id string) (Installed, error) {
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
	return Installed{Manifest: m, Directory: dir, limits: s.limits}, nil
}

// entry is one name found directly under a store root.
type entry struct {
	name string // directory name as stored
	id   string // package ID when name is a valid package directory
	err  error  // why name is not a package directory
}

// scan lists the names under the root without reading or verifying any
// package. A missing root has none. Staging directories (leading ".") are
// skipped. The caller holds s.mu.
func (s *Store) scan() ([]entry, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []entry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		item := entry{name: e.Name()}
		if !e.IsDir() {
			item.err = fmt.Errorf("%w: unexpected entry %q", plugin.ErrInvalid, e.Name())
		} else if id := strings.ReplaceAll(e.Name(), "%2f", "/"); !validID(id) {
			item.err = fmt.Errorf("%w: invalid package directory %q", plugin.ErrInvalid, e.Name())
		} else {
			item.id = id
		}
		out = append(out, item)
	}
	return out, nil
}

// entryIDs returns the package IDs named by the root's directories without
// reading or verifying their content. It fails on an entry that is not a
// package directory. The caller holds s.mu.
func (s *Store) entryIDs() ([]string, error) {
	entries, err := s.scan()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.err != nil {
			return nil, e.err
		}
		ids = append(ids, e.id)
	}
	return ids, nil
}

func (s *Store) listIDs() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entryIDs()
}

// List verifies every installed package and fails closed on the first bad one.
// A missing root is an empty store. Staging directories (leading ".") are skipped.
func (s *Store) List() ([]Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.entryIDs()
	if err != nil {
		return nil, err
	}
	var out []Installed
	for _, id := range ids {
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

// ListUnverified returns every installed package's manifest and directory
// without verifying any content, and reports the packages it could not read
// separately. It is for a caller that needs to know what is installed, such as
// a catalog or a picker, and cannot afford to hash every artifact: it costs one
// directory listing and one small manifest read per package, whatever the
// size of the artifacts, where List hashes them all.
//
// THE RESULT IS UNVERIFIED. A returned package has a manifest that decodes and
// validates and a directory that matches it, nothing more: artifacts may be
// missing, changed, linked, unlisted or oversized. Content is checked only by
// Installed.Verify, by host.Open (which verifies before it launches) and by
// ArtifactPath and Executable (which verify the artifact they return). Do not
// launch, load or trust anything from this result directly, and do not take
// its presence as proof that the package would pass List or Load. Name, Version,
// Supports and IsExecutable read only the manifest and are safe to use.
//
// A package that cannot be read does not hide the others. Each one is reported
// in the second result, in the same form Verify uses: its ID (the directory name
// when that is not a valid ID), its directory and why, which is a manifest that
// does not decode or validate, a directory that does not match its manifest, or
// an entry that is not a package directory. Every Check returned here has a
// non-nil Err. Both results are sorted by ID. The error is only for a root that
// cannot be read; a missing root is empty. Staging directories (leading ".") are
// skipped.
//
// List and Load are unchanged and still fail closed.
func (s *Store) ListUnverified() ([]Installed, []Check, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	installed, problems, _, err := s.manifestsAll(nil)
	return installed, problems, err
}

// manifestsAll reads the manifest of each entry, skipping any ID for which skip
// returns true. It returns the packages read, a Check for each entry that could
// not be read, and every valid package ID found, skipped or not. The caller
// holds s.mu.
func (s *Store) manifestsAll(skip func(id string) bool) ([]Installed, []Check, []string, error) {
	entries, err := s.scan()
	if err != nil {
		return nil, nil, nil, err
	}
	var installed []Installed
	var problems []Check
	var ids []string
	for _, e := range entries {
		dir := filepath.Join(s.root, e.name)
		if e.err != nil {
			problems = append(problems, Check{ID: e.name, Directory: dir, Err: e.err})
			continue
		}
		ids = append(ids, e.id)
		if skip != nil && skip(e.id) {
			continue
		}
		p, err := s.loadManifest(e.id)
		if err != nil {
			problems = append(problems, Check{ID: e.id, Directory: dir, Err: err})
			continue
		}
		installed = append(installed, p)
	}
	sort.Slice(installed, func(i, j int) bool {
		return installed[i].Manifest.Descriptor.Identity.ID < installed[j].Manifest.Descriptor.Identity.ID
	})
	sort.Slice(problems, func(i, j int) bool { return problems[i].ID < problems[j].ID })
	return installed, problems, ids, nil
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
	if err := (Installed{Manifest: m, Directory: source, limits: s.limits}).Verify(); err != nil {
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
		if err := copyArtifact(source, staging, a, s.limits, mode); err != nil {
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
	p := Installed{Manifest: m, Directory: staging, limits: s.limits}
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

func copyArtifact(srcRoot, dstRoot string, a packagekit.Artifact, limits Limits, mode os.FileMode) error {
	if a.Kind == packagekit.ArtifactTree {
		return copyTree(srcRoot, dstRoot, a, limits)
	}
	max := limits.MaxArtifactBytes
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
	var m packagekit.Manifest
	if err := plugin.Decode(data, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
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
	limits := p.limits.normalize()
	listed := map[string]bool{ManifestName: true}
	trees := map[string]bool{}
	for _, a := range p.Manifest.Artifacts {
		if a.Kind == packagekit.ArtifactTree {
			path, err := secureDirectory(p.Directory, a.Path)
			if err == nil {
				err = verifyTree(path, a.SHA256, limits)
			}
			if err != nil {
				return fmt.Errorf("artifact %s: %w", a.Name, err)
			}
			trees[strings.ToLower(a.Path)] = true
			continue
		}
		path, err := securePath(p.Directory, a.Path)
		if err != nil {
			return fmt.Errorf("artifact %s: %w", a.Name, err)
		}
		if err := checkDigest(path, p.Directory, a.Path, a.SHA256, limits.MaxArtifactBytes); err != nil {
			return fmt.Errorf("artifact %s: %w", a.Name, err)
		}
		listed[strings.ToLower(a.Path)] = true
	}
	if _, err := securePath(p.Directory, ManifestName); err != nil {
		return err
	}
	return filepathWalkExtra(p.Directory, listed, trees)
}

func filepathWalkExtra(root string, listed, trees map[string]bool) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.ToSlash(rel))
		if d.IsDir() {
			if trees[key] {
				return filepath.SkipDir // verified as a whole by its digest
			}
			return nil
		}
		if !listed[key] {
			return fmt.Errorf("%w: unlisted file %q", plugin.ErrInvalid, filepath.ToSlash(rel))
		}
		return nil
	})
}

// checkDigest verifies one file artifact: it must not exceed max bytes, and the
// digest of the file (addressed under root, never following links)
// must equal digest. path is the securePath result for rel under root.
func checkDigest(path, root, rel, digest string, max int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Size() > max {
		return fmt.Errorf("%w: exceeds size limit", plugin.ErrInvalid)
	}
	got, err := plugin.ArtifactDigest(root, rel, false)
	if err != nil {
		return err
	}
	if got != digest {
		return plugin.ErrMismatch
	}
	return nil
}

// Digest returns the directory digest of the verified package directory, for
// consumers that record trust against exact installed content.
func (p Installed) Digest() (string, error) {
	if err := p.Verify(); err != nil {
		return "", err
	}
	return plugin.DirectoryDigest(p.Directory)
}

// ArtifactPath verifies the named artifact and returns its filesystem path
// (under Directory): a file, or for a tree artifact its directory. Use it to
// locate source files, a vendored tree or an executable.
func (p Installed) ArtifactPath(name string) (string, error) {
	for _, a := range p.Manifest.Artifacts {
		if a.Name == name {
			limits := p.limits.normalize()
			if a.Kind == packagekit.ArtifactTree {
				path, err := secureDirectory(p.Directory, a.Path)
				if err == nil {
					err = verifyTree(path, a.SHA256, limits)
				}
				if err != nil {
					return "", fmt.Errorf("artifact %s: %w", name, err)
				}
				return path, nil
			}
			path, err := securePath(p.Directory, a.Path)
			if err != nil {
				return "", err
			}
			if err := checkDigest(path, p.Directory, a.Path, a.SHA256, limits.MaxArtifactBytes); err != nil {
				return "", fmt.Errorf("artifact %s: %w", name, err)
			}
			return path, nil
		}
	}
	return "", plugin.ErrNotFound
}

// Name is the package identity ID.
func (p Installed) Name() string { return p.Manifest.Name() }

// Version is the package release version, which may be empty.
func (p Installed) Version() string { return p.Manifest.Version() }

// Supports reports whether the package serves an exact contract version.
func (p Installed) Supports(name, version string) bool { return p.Manifest.Supports(name, version) }

// IsExecutable reports whether the package declares an entrypoint, as opposed
// to shipping source files the consumer runs under a runtime it owns.
func (p Installed) IsExecutable() bool { return len(p.Manifest.Entrypoints) > 0 }

// Executable returns the digest-verified path of the first entrypoint's
// artifact. A package without entrypoints returns plugin.ErrNotFound.
func (p Installed) Executable() (string, error) {
	if !p.IsExecutable() {
		return "", fmt.Errorf("%w: package declares no entrypoint", plugin.ErrNotFound)
	}
	return p.ArtifactPath(p.Manifest.Entrypoints[0].Artifact)
}

// WriteManifest validates m and writes it as ManifestName in dir, for authors
// assembling a package directory that Install will copy.
func WriteManifest(dir string, m packagekit.Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ManifestName), append(encoded, '\n'), 0o600)
}

// secureDirectory resolves a slash path under root to a directory, refusing
// symbolic links at every component.
func secureDirectory(root, name string) (string, error) {
	current := root
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", plugin.ErrInvalid
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%w: tree path must be a plain directory", plugin.ErrInvalid)
		}
	}
	return current, nil
}

// verifyTree enforces the tree bounds and compares plugin.DirectoryDigest with
// the manifest. Only regular files and directories are allowed.
func verifyTree(dir, digest string, limits Limits) error {
	files, total := 0, int64(0)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%w: trees may hold only regular files", plugin.ErrInvalid)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		total += info.Size()
		if files > limits.MaxTreeFiles || total > limits.MaxTreeBytes || info.Size() > limits.MaxArtifactBytes {
			return fmt.Errorf("%w: tree exceeds its size limits", plugin.ErrInvalid)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if files == 0 {
		return fmt.Errorf("%w: tree holds no files", plugin.ErrInvalid)
	}
	actual, err := plugin.DirectoryDigest(dir)
	if err != nil {
		return err
	}
	if actual != digest {
		return plugin.ErrMismatch
	}
	return nil
}

// copyTree copies a tree artifact into dstRoot, enforcing the bounds while it
// copies. Directories are created 0700 and files 0600, or 0700 when the source
// file has its owner-execute bit (modes are not covered by the digest). The
// caller verifies the staged copy against the manifest digest afterwards.
func copyTree(srcRoot, dstRoot string, a packagekit.Artifact, limits Limits) error {
	source, err := secureDirectory(srcRoot, a.Path)
	if err != nil {
		return err
	}
	destination := filepath.Join(dstRoot, filepath.FromSlash(a.Path))
	files, total := 0, int64(0)
	return filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%w: trees may hold only regular files", plugin.ErrInvalid)
		}
		files++
		if files > limits.MaxTreeFiles {
			return fmt.Errorf("%w: tree exceeds its file limit", plugin.ErrInvalid)
		}
		linkInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := in.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !os.SameFile(linkInfo, info) {
			return fmt.Errorf("%w: file changed while copying", plugin.ErrInvalid)
		}
		mode := os.FileMode(0o600)
		if info.Mode().Perm()&0o100 != 0 {
			mode = 0o700
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, limits.MaxArtifactBytes+1))
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		total += n
		if n > limits.MaxArtifactBytes || total > limits.MaxTreeBytes {
			return fmt.Errorf("%w: tree exceeds its size limits", plugin.ErrInvalid)
		}
		// Keep the owner-execute bit even under a restrictive umask.
		return os.Chmod(target, mode)
	})
}

// Check is the verification result for one entry under a store root.
type Check struct {
	// ID is the package ID, or the directory name when it is not a valid one.
	ID string
	// Directory is where the entry lives.
	Directory string
	// Err is why the entry failed: a manifest that does not decode or validate,
	// an artifact that is missing, changed, linked or oversized, an unlisted
	// file, a directory that does not match its manifest, or an entry that is
	// not a package directory. Nil means the package verified.
	Err error
}

// OK reports whether the entry verified.
func (c Check) OK() bool { return c.Err == nil }

// Verify checks every package under the root and reports each one. Unlike List
// it does not stop at the first bad package, so a caller can show everything
// that is wrong. It runs nothing and changes nothing. Results are sorted by ID.
// The error is only for a root that cannot be read; a missing root is empty.
func (s *Store) Verify() ([]Check, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	checks, _, err := s.verifyAll(nil)
	return checks, err
}

// verifyAll verifies each entry, skipping any ID for which skip returns true.
// It also returns every valid package ID found, skipped or not. The caller
// holds s.mu.
func (s *Store) verifyAll(skip func(id string) bool) ([]Check, []string, error) {
	entries, err := s.scan()
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	out := make([]Check, 0, len(entries))
	for _, e := range entries {
		dir := filepath.Join(s.root, e.name)
		if e.err != nil {
			out = append(out, Check{ID: e.name, Directory: dir, Err: e.err})
			continue
		}
		ids = append(ids, e.id)
		if skip != nil && skip(e.id) {
			continue
		}
		_, err := s.load(e.id)
		out = append(out, Check{ID: e.id, Directory: dir, Err: err})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, ids, nil
}
