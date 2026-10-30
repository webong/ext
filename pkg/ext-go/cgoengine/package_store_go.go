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
