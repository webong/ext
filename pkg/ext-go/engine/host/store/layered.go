package store

import (
	"errors"
	"fmt"
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
// needs at least one store and rejects nil entries and repeated roots.
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

// Load returns the verified package from the first store that holds id.
// ErrNotFound means no store holds it.
func (l *Layered) Load(id string) (Installed, error) {
	if !validID(id) {
		return Installed{}, plugin.ErrInvalid
	}
	for _, s := range l.stores {
		p, err := s.Load(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return p, err
	}
	return Installed{}, ErrNotFound
}

// List verifies and returns one package per ID, sorted by ID, each from the
// highest-priority store that holds it. It fails closed on the first bad
// package or unexpected entry in any store. A missing root is empty.
func (l *Layered) List() ([]Installed, error) {
	winner := map[string]*Store{}
	for _, s := range l.stores {
		ids, err := s.listIDs()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, taken := winner[id]; !taken {
				winner[id] = s
			}
		}
	}
	ids := make([]string, 0, len(winner))
	for id := range winner {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Installed, 0, len(ids))
	for _, id := range ids {
		p, err := winner[id].Load(id)
		if err != nil {
			return nil, fmt.Errorf("package %s: %w", id, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Verify checks what Load and List would serve and reports every failure
// instead of stopping at the first. A package is checked in the highest-priority
// store that holds it; shadowed copies are skipped, as in List. An entry that is
// not a package directory is reported in the store it sits in. It runs nothing
// and changes nothing. Results are sorted by ID, then by store priority. The
// error is only for a root that cannot be read.
func (l *Layered) Verify() ([]Check, error) {
	claimed := map[string]bool{}
	var out []Check
	for _, s := range l.stores {
		s.mu.Lock()
		checks, ids, err := s.verifyAll(func(id string) bool { return claimed[id] })
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			claimed[id] = true
		}
		out = append(out, checks...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
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
	claimed := map[string]bool{}
	var installed []Installed
	var problems []Check
	for _, s := range l.stores {
		s.mu.Lock()
		got, bad, ids, err := s.manifestsAll(func(id string) bool { return claimed[id] })
		s.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
		for _, id := range ids {
			claimed[id] = true
		}
		installed = append(installed, got...)
		problems = append(problems, bad...)
	}
	sort.SliceStable(installed, func(i, j int) bool {
		return installed[i].Manifest.Descriptor.Identity.ID < installed[j].Manifest.Descriptor.Identity.ID
	})
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].ID < problems[j].ID })
	return installed, problems, nil
}
