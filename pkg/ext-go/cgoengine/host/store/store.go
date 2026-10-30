// Package store installs, upgrades, lists and removes reviewed packages under
// a caller-supplied root. A package is a packagekit manifest plus the artifacts
// it lists. The store has no default location and no product vocabulary; it
// copies and re-verifies content but never launches it or grants authority.
// Concurrent mutation from several processes is the caller's responsibility.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/pkg/ext-go/cgoengine/packages"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/package"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ManifestName is the manifest file at the top of every package directory: the
// ext.package/v1 document. A package.json at the top level is an ordinary file,
// which a package may ship as an artifact (an npm package, say) like any other;
// only this name is read as the manifest. An artifact may not use this path,
// whatever its case, and a tree may hold a file of this name of its own.
const ManifestName = "plugin.json"

// DefaultMaxArtifactBytes bounds one artifact when Limits does not.
const DefaultMaxArtifactBytes int64 = 4 << 30

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

// CopyOptions lets a consumer retain executable helper artifacts in addition
// to declared entrypoints. The default install keeps non-entrypoint files
// non-executable; hosts using this option must authorize those helpers.
type CopyOptions struct {
	PreserveExecutableArtifacts bool
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
	return s.engine(func(e *cgoengine.PackageStore) (Installed, error) {
		manifest, err := e.Load(id)
		if err != nil {
			return Installed{}, storeError(err)
		}
		return s.installed(manifest)
	})
}

// List verifies every installed package and fails closed on the first bad one.
// A missing root is an empty store. Staging directories (leading ".") are skipped.
func (s *Store) List() ([]Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.records(func(e *cgoengine.PackageStore) ([]cgoengine.PackageRecord, error) { return e.List() })
	if err != nil {
		return nil, err
	}
	return s.installedAll(records)
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
// A returned Installed carries the manifest read at listing time, and Verify,
// host.Open and ArtifactPath check content against that manifest, not against
// whatever plugin.json holds later. If the manifest on disk changes after the
// listing, list again to see the change.
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
	e, err := s.open()
	if err != nil {
		return nil, nil, err
	}
	defer e.Close()
	records, checks, err := e.ListUnverified()
	if err != nil {
		return nil, nil, storeError(err)
	}
	installed, err := s.installedAll(records)
	return installed, packageChecks(checks), err
}

// Install copies the package in source (a directory holding ManifestName and
// the listed artifacts) into the store. It never replaces an existing package.
func (s *Store) Install(source string) (Installed, error) {
	return s.InstallWithOptions(source, CopyOptions{})
}

// InstallWithOptions installs a verified package using explicit copy policy.
func (s *Store) InstallWithOptions(source string, options CopyOptions) (Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(source, options, (*cgoengine.PackageStore).Install)
}

// Upgrade replaces an installed package with a new revision of the same ID.
// Contract names and versions must match; operations may change, so the caller
// must re-review authority. The previous installation survives any failure.
func (s *Store) Upgrade(source string) (Installed, error) {
	return s.UpgradeWithOptions(source, CopyOptions{})
}

// UpgradeWithOptions upgrades a package using explicit copy policy.
func (s *Store) UpgradeWithOptions(source string, options CopyOptions) (Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(source, options, (*cgoengine.PackageStore).Upgrade)
}

// Replace installs or replaces a package of the same ID, including changes to
// its contracts or assets. Consumers must re-review trust after replacement.
func (s *Store) Replace(source string) (Installed, error) {
	return s.ReplaceWithOptions(source, CopyOptions{})
}

// ReplaceWithOptions is Replace with explicit copy policy.
func (s *Store) ReplaceWithOptions(source string, options CopyOptions) (Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(source, options, (*cgoengine.PackageStore).Replace)
}

// Remove deletes one installed package directory. It never follows links.
func (s *Store) Remove(id string) error {
	if !validID(id) {
		return plugin.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.open()
	if err != nil {
		return err
	}
	defer e.Close()
	return storeError(e.Remove(id))
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
	if err := packages.ValidateManifest(p.Manifest); err != nil {
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
// C engine's digest of the file (addressed under root, never following links)
// must equal digest. path is the securePath result for rel under root.
func checkDigest(path, root, rel, digest string, max int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Size() > max {
		return fmt.Errorf("%w: exceeds size limit", plugin.ErrInvalid)
	}
	got, err := cgoengine.ArtifactDigest(root, rel, false)
	if err != nil {
		return err
	}
	if got != digest {
		return plugin.ErrMismatch
	}
	return nil
}

// Digest returns the C engine's directory digest of the verified package directory, for
// consumers that record trust against exact installed content.
func (p Installed) Digest() (string, error) {
	if err := p.Verify(); err != nil {
		return "", err
	}
	return cgoengine.DirectoryDigest(p.Directory)
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
	if err := packages.ValidateManifest(m); err != nil {
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
	actual, err := cgoengine.DirectoryDigest(dir)
	if err != nil {
		return err
	}
	if actual != digest {
		return plugin.ErrMismatch
	}
	return nil
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
	e, err := s.open()
	if err != nil {
		return nil, err
	}
	defer e.Close()
	checks, err := e.Verify()
	if err != nil {
		return nil, storeError(err)
	}
	return packageChecks(checks), nil
}
