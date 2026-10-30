package store

import (
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cgoengine"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"path/filepath"
	"sort"
)

// Layered is a read-only view over several stores. Earlier stores shadow later
// ones: a package ID is served from the first store that holds it, so a user
// installation can override a system one. It has no default roots and no
// product vocabulary; the consumer decides which roots exist and their order,
// for example a user root followed by a read-only system root.
//
// Layered never writes. Install, upgrade and remove stay on the Store the
// caller owns, so a system root is never created or modified through it.
//
// Shadowing is by ID only. If the winning copy fails verification, Load and
// List fail closed instead of falling back to a copy in a later store: a
// damaged override must not silently turn into a different package. A shadowed
// copy is never read or verified.
type Layered struct {
	stores []*Store
}

// NewLayered returns a view over stores in priority order, highest first. It
// needs at least one store and rejects nil entries, repeated roots and stores
// whose limits differ: the view applies one set of limits to all its roots.
func NewLayered(stores ...*Store) (*Layered, error) {
	if len(stores) == 0 {
		return nil, plugin.ErrInvalid
	}
	seen := map[string]bool{}
	for _, s := range stores {
		if s == nil {
			return nil, plugin.ErrInvalid
		}
		root := filepath.Clean(s.root)
		if seen[root] {
			return nil, fmt.Errorf("%w: store root %q repeated", plugin.ErrInvalid, s.root)
		}
		seen[root] = true
		if s.limits != stores[0].limits {
			return nil, fmt.Errorf("%w: store %q has other limits than the first", plugin.ErrInvalid, s.root)
		}
	}
	return &Layered{stores: append([]*Store(nil), stores...)}, nil
}

// Roots returns the store roots in priority order. An Installed package's
// Directory lies under the root of the store that served it.
func (l *Layered) Roots() []string {
	roots := make([]string, len(l.stores))
	for i, s := range l.stores {
		roots[i] = s.root
	}
	return roots
}

// view runs call on the engine's layered view over the roots. The stores' locks are held, in the
// order of their roots whatever the priority, so that two views over the same stores cannot wait on
// each other, and a Store's installs and removals in this process do not interleave with the read.
func (l *Layered) view(call func(*cgoengine.LayeredPackageStore) error) error {
	order := append([]*Store(nil), l.stores...)
	sort.Slice(order, func(i, j int) bool { return order[i].root < order[j].root })
	for _, s := range order {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	v, err := cgoengine.OpenLayeredPackageStore(l.Roots(), packageLimits(l.stores[0].limits))
	if err != nil {
		return err
	}
	defer v.Close()
	return call(v)
}

// Load returns the verified package from the first store that holds id.
// ErrNotFound means no store holds it.
func (l *Layered) Load(id string) (Installed, error) {
	if !validID(id) {
		return Installed{}, plugin.ErrInvalid
	}
	var out Installed
	err := l.view(func(v *cgoengine.LayeredPackageStore) error {
		record, err := v.Load(id)
		if err != nil {
			return storeError(err)
		}
		installed, err := installedRecords([]cgoengine.PackageRecord{record}, l.stores[0].limits)
		if err != nil {
			return err
		}
		out = installed[0]
		return nil
	})
	return out, err
}

// List verifies and returns one package per ID, sorted by ID, each from the
// highest-priority store that holds it. It fails closed on the first bad
// package or unexpected entry in any store. A missing root is empty.
func (l *Layered) List() ([]Installed, error) {
	var out []Installed
	err := l.view(func(v *cgoengine.LayeredPackageStore) error {
		records, err := v.List()
		if err != nil {
			return storeError(err)
		}
		out, err = installedRecords(records, l.stores[0].limits)
		return err
	})
	return out, err
}

// Verify checks what Load and List would serve and reports every failure
// instead of stopping at the first. A package is checked in the highest-priority
// store that holds it; shadowed copies are skipped, as in List. An entry that is
// not a package directory is reported in the store it sits in. It runs nothing
// and changes nothing. Results are sorted by ID, then by store priority. The
// error is only for a root that cannot be read.
func (l *Layered) Verify() ([]Check, error) {
	var out []Check
	err := l.view(func(v *cgoengine.LayeredPackageStore) error {
		checks, err := v.Verify()
		if err != nil {
			return storeError(err)
		}
		out = packageChecks(checks)
		return nil
	})
	return out, err
}

// ListUnverified returns one package's manifest per ID, each from the
// highest-priority store that holds it, without verifying any content, and
// reports the packages it could not read separately. See Store.ListUnverified:
// the result is unverified, content is checked only by Installed.Verify,
// host.Open and ArtifactPath, and that check is against the manifest read at
// listing time. Shadowing is as in List and Verify: an ID belongs
// to the highest-priority store whose directory holds it, even if that copy
// cannot be read, in which case it is reported rather than replaced by a copy in
// a later store; a shadowed copy is never read. An entry that is not a package
// directory is reported in the store it sits in. Results are sorted by ID. The
// error is only for a root that cannot be read.
func (l *Layered) ListUnverified() ([]Installed, []Check, error) {
	var installed []Installed
	var problems []Check
	err := l.view(func(v *cgoengine.LayeredPackageStore) error {
		records, checks, err := v.ListUnverified()
		if err != nil {
			return storeError(err)
		}
		installed, err = installedRecords(records, l.stores[0].limits)
		problems = packageChecks(checks)
		return err
	})
	return installed, problems, err
}
