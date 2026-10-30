package store

import (
	"errors"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	"github.com/webong/ext/pkg/ext-go/cgoengine/packages"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

// The package operations of a Store (install, upgrade, replace, remove, load, list, verify and the
// unverified listing) run in the C engine's package store, cgoengine.PackageStore, or in the
// pure-Go store behind the same type in a build without the C engine. A Store holds no handle: each
// call opens one on the root and closes it again, so a Store keeps nothing open and, as before,
// needs no Close. Installed still re-verifies content on use, in this package.

func packageLimits(l Limits) cgoengine.PackageStoreLimits {
	return cgoengine.PackageStoreLimits{MaxArtifactBytes: l.MaxArtifactBytes, MaxTreeFiles: l.MaxTreeFiles, MaxTreeBytes: l.MaxTreeBytes}
}

// open returns the engine's package store on the root. The caller holds s.mu and closes it.
func (s *Store) open() (*cgoengine.PackageStore, error) {
	return cgoengine.OpenPackageStore(s.root, packageLimits(s.limits))
}

// engine runs call on the engine's package store. The caller holds s.mu.
func (s *Store) engine(call func(*cgoengine.PackageStore) (Installed, error)) (Installed, error) {
	e, err := s.open()
	if err != nil {
		return Installed{}, err
	}
	defer e.Close()
	return call(e)
}

// put runs one of the engine's install, upgrade and replace on source. The caller holds s.mu.
func (s *Store) put(source string, options CopyOptions, op func(*cgoengine.PackageStore, string, bool) ([]byte, error)) (Installed, error) {
	return s.engine(func(e *cgoengine.PackageStore) (Installed, error) {
		manifest, err := op(e, source, options.PreserveExecutableArtifacts)
		if err != nil {
			return Installed{}, storeError(err)
		}
		return s.installed(manifest)
	})
}

// records runs one of the engine's listings. The caller holds s.mu.
func (s *Store) records(call func(*cgoengine.PackageStore) ([]cgoengine.PackageRecord, error)) ([]cgoengine.PackageRecord, error) {
	e, err := s.open()
	if err != nil {
		return nil, err
	}
	defer e.Close()
	records, err := call(e)
	return records, storeError(err)
}

// installed decodes a plugin.json the engine returned into the package under this store's root.
func (s *Store) installed(manifest []byte) (Installed, error) {
	m, err := decodeManifest(manifest)
	if err != nil {
		return Installed{}, err
	}
	return Installed{Manifest: m, Directory: s.dir(m.Descriptor.Identity.ID), limits: s.limits}, nil
}

func (s *Store) installedAll(records []cgoengine.PackageRecord) ([]Installed, error) {
	return installedRecords(records, s.limits)
}

func installedRecords(records []cgoengine.PackageRecord, limits Limits) ([]Installed, error) {
	out := make([]Installed, 0, len(records))
	for _, r := range records {
		m, err := decodeManifest(r.Manifest)
		if err != nil {
			return nil, err
		}
		out = append(out, Installed{Manifest: m, Directory: r.Directory, limits: limits})
	}
	return out, nil
}

func decodeManifest(data []byte) (packagekit.Manifest, error) {
	var m packagekit.Manifest
	if err := plugin.Decode(data, &m); err != nil {
		return m, err
	}
	return m, packages.ValidateManifest(m)
}

func packageChecks(checks []cgoengine.PackageCheck) []Check {
	out := make([]Check, 0, len(checks))
	for _, c := range checks {
		out = append(out, Check{ID: c.ID, Directory: c.Directory, Err: storeError(c.Err)})
	}
	return out
}

// storeError gives an engine error this package's own sentinels for an id that is installed already
// and one that is not installed.
func storeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cgoengine.ErrPackageExists):
		return ErrExists
	case errors.Is(err, plugin.ErrNotFound):
		return ErrNotFound
	}
	return err
}
