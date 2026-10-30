package cgoengine

import (
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrPackageExists is returned by an install over an installed package id.
var ErrPackageExists = errors.New("plugin package already installed")

// PackageStoreLimits are optional size bounds for a PackageStore: one artifact file (a file inside
// a tree counts too), the files in one tree and the bytes of one tree. Zero takes the default.
type PackageStoreLimits struct {
	MaxArtifactBytes int64
	MaxTreeFiles     int
	MaxTreeBytes     int64
}

// goPackageStore is the package store of the pure-Go engine behind the PackageStore methods. It is
// the PackageStore of a build without the C engine, and the baseline the C store is held to.
type goPackageStore struct {
	s    *store.Store
	root string
}

func openGoPackageStore(root string, limits PackageStoreLimits) (*goPackageStore, error) {
	s, err := store.New(root, store.Limits{MaxArtifactBytes: limits.MaxArtifactBytes, MaxTreeFiles: limits.MaxTreeFiles, MaxTreeBytes: limits.MaxTreeBytes})
	if err != nil {
		return nil, err
	}
	return &goPackageStore{s: s, root: root}, nil
}

// Close releases the store. The pure-Go store holds nothing.
func (g *goPackageStore) Close() {}

func packageStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrExists):
		return fmt.Errorf("%w: %v", ErrPackageExists, err)
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %v", plugin.ErrNotFound, err)
	}
	return err
}

func (g *goPackageStore) manifest(p store.Installed, err error) ([]byte, error) {
	if err != nil {
		return nil, packageStoreError(err)
	}
	return os.ReadFile(filepath.Join(p.Directory, store.ManifestName))
}

func copyOptions(preserveExecutable bool) store.CopyOptions {
	return store.CopyOptions{PreserveExecutableArtifacts: preserveExecutable}
}

// Install copies the package in source into the store, never over an installed id, and returns the
// installed plugin.json.
func (g *goPackageStore) Install(source string, preserveExecutable bool) ([]byte, error) {
	return g.manifest(g.s.InstallWithOptions(source, copyOptions(preserveExecutable)))
}

// Upgrade replaces an installed package with a new revision of the same id and contracts.
func (g *goPackageStore) Upgrade(source string, preserveExecutable bool) ([]byte, error) {
	return g.manifest(g.s.UpgradeWithOptions(source, copyOptions(preserveExecutable)))
}

// Replace installs or replaces the package of the source's id, whatever its contracts.
func (g *goPackageStore) Replace(source string, preserveExecutable bool) ([]byte, error) {
	return g.manifest(g.s.ReplaceWithOptions(source, copyOptions(preserveExecutable)))
}

// Remove deletes one installed package.
func (g *goPackageStore) Remove(id string) error { return packageStoreError(g.s.Remove(id)) }

// Load reads and verifies one installed package and returns its plugin.json.
func (g *goPackageStore) Load(id string) ([]byte, error) { return g.manifest(g.s.Load(id)) }

// IDs lists the installed package ids in byte order, without reading or verifying any package.
func (g *goPackageStore) IDs() ([]string, error) {
	entries, err := os.ReadDir(g.root)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			return nil, fmt.Errorf("%w: unexpected entry %q", plugin.ErrInvalid, e.Name())
		}
		id := strings.ReplaceAll(e.Name(), "%2f", "/")
		if (plugin.Identity{ID: id, Revision: "r"}).Validate() != nil {
			return nil, fmt.Errorf("%w: invalid package directory %q", plugin.ErrInvalid, e.Name())
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// PackageRecord is one package a store or layered view serves: its id, the directory it lives in,
// the position of the root that serves it (always 0 for a single store) and its plugin.json.
type PackageRecord struct {
	ID        string
	Directory string
	Layer     int
	Manifest  []byte
}

// PackageCheck is the result of verifying one entry below a root: a package id, or the directory
// name when it is not a package directory. Err is nil when the entry verified.
type PackageCheck struct {
	ID        string
	Directory string
	Layer     int
	Err       error
}

func (g *goPackageStore) records(installed []store.Installed, layerOf func(string) int) ([]PackageRecord, error) {
	out := make([]PackageRecord, 0, len(installed))
	for _, p := range installed {
		manifest, err := os.ReadFile(filepath.Join(p.Directory, store.ManifestName))
		if err != nil {
			return nil, err
		}
		out = append(out, PackageRecord{ID: p.Manifest.Descriptor.Identity.ID, Directory: p.Directory, Layer: layerOf(p.Directory), Manifest: manifest})
	}
	return out, nil
}

func packageChecks(checks []store.Check, layerOf func(string) int) []PackageCheck {
	out := make([]PackageCheck, 0, len(checks))
	for _, c := range checks {
		out = append(out, PackageCheck{ID: c.ID, Directory: c.Directory, Layer: layerOf(c.Directory), Err: packageStoreError(c.Err)})
	}
	return out
}

func layerZero(string) int { return 0 }

// List verifies every installed package and returns them sorted by id. It fails on the first entry
// that is not a package directory and on the first package that does not verify.
func (g *goPackageStore) List() ([]PackageRecord, error) {
	installed, err := g.s.List()
	if err != nil {
		return nil, packageStoreError(err)
	}
	return g.records(installed, layerZero)
}

// Verify checks every entry below the root and reports each, sorted by id.
func (g *goPackageStore) Verify() ([]PackageCheck, error) {
	checks, err := g.s.Verify()
	if err != nil {
		return nil, packageStoreError(err)
	}
	return packageChecks(checks, layerZero), nil
}

// ListUnverified returns the packages whose manifest reads and the entries that could not be read,
// without reading any artifact. The packages are unverified.
func (g *goPackageStore) ListUnverified() ([]PackageRecord, []PackageCheck, error) {
	installed, problems, err := g.s.ListUnverified()
	if err != nil {
		return nil, nil, packageStoreError(err)
	}
	records, err := g.records(installed, layerZero)
	return records, packageChecks(problems, layerZero), err
}

// goLayeredPackageStore is a read-only view over several package roots of the pure-Go engine, the
// LayeredPackageStore of a build without the C engine and the baseline the C view is held to.
type goLayeredPackageStore struct {
	view  *store.Layered
	roots []string
	one   goPackageStore
}

func openGoLayeredPackageStore(roots []string, limits PackageStoreLimits) (*goLayeredPackageStore, error) {
	stores := make([]*store.Store, 0, len(roots))
	for _, root := range roots {
		s, err := store.New(root, store.Limits{MaxArtifactBytes: limits.MaxArtifactBytes, MaxTreeFiles: limits.MaxTreeFiles, MaxTreeBytes: limits.MaxTreeBytes})
		if err != nil {
			return nil, err
		}
		stores = append(stores, s)
	}
	view, err := store.NewLayered(stores...)
	if err != nil {
		return nil, err
	}
	return &goLayeredPackageStore{view: view, roots: append([]string(nil), roots...)}, nil
}

// Close releases the view. The pure-Go view holds nothing.
func (g *goLayeredPackageStore) Close() {}

// Roots returns the roots in priority order, as they were given.
func (g *goLayeredPackageStore) Roots() []string { return append([]string(nil), g.roots...) }

func (g *goLayeredPackageStore) layerOf(dir string) int {
	parent := filepath.Dir(dir)
	for i, root := range g.roots {
		if filepath.Clean(root) == parent {
			return i
		}
	}
	return -1
}

// Load reads and verifies the package from the first root that holds id. plugin.ErrNotFound means
// no root does. A damaged winning copy is an error, never a fall back to a lower root.
func (g *goLayeredPackageStore) Load(id string) (PackageRecord, error) {
	p, err := g.view.Load(id)
	if err != nil {
		return PackageRecord{}, packageStoreError(err)
	}
	records, err := g.one.records([]store.Installed{p}, g.layerOf)
	if err != nil {
		return PackageRecord{}, err
	}
	return records[0], nil
}

// List verifies and returns one package per id from the highest root that holds it.
func (g *goLayeredPackageStore) List() ([]PackageRecord, error) {
	installed, err := g.view.List()
	if err != nil {
		return nil, packageStoreError(err)
	}
	return g.one.records(installed, g.layerOf)
}

// Verify checks what Load and List would serve and reports every failure.
func (g *goLayeredPackageStore) Verify() ([]PackageCheck, error) {
	checks, err := g.view.Verify()
	if err != nil {
		return nil, packageStoreError(err)
	}
	return packageChecks(checks, g.layerOf), nil
}

// ListUnverified returns the manifests of the packages each root serves without reading any
// artifact, and the entries that could not be read. The packages are unverified.
func (g *goLayeredPackageStore) ListUnverified() ([]PackageRecord, []PackageCheck, error) {
	installed, problems, err := g.view.ListUnverified()
	if err != nil {
		return nil, nil, packageStoreError(err)
	}
	records, err := g.one.records(installed, g.layerOf)
	return records, packageChecks(problems, g.layerOf), err
}
