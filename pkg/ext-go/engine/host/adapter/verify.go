package adapter

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// TrustState is how an installed adapter's content compares with the digest
// recorded when it was trusted.
type TrustState string

const (
	// TrustTrusted means the content matches the recorded digest.
	TrustTrusted TrustState = "trusted"
	// TrustUntrusted means no digest was ever recorded for the adapter.
	TrustUntrusted TrustState = "untrusted"
	// TrustChanged means a digest was recorded and the content no longer matches.
	TrustChanged TrustState = "changed"
)

// TrustState compares a's current content with the recorded digest. IsTrusted
// reports only match or no match; this separates an adapter nobody reviewed
// from one that was reviewed and has since been modified.
func (s *Store) TrustState(a *Adapter) (TrustState, error) {
	trust, err := s.readTrust()
	if err != nil {
		return "", err
	}
	return trustStateOf(trust, a)
}

func trustStateOf(trust map[string]string, a *Adapter) (TrustState, error) {
	descriptor, err := a.PluginDescriptor()
	if err != nil {
		return "", err
	}
	recorded, ok := trust[a.Manifest.Name]
	switch {
	case !ok:
		return TrustUntrusted, nil
	case recorded == strings.TrimPrefix(descriptor.Identity.Revision, "sha256:"):
		return TrustTrusted, nil
	default:
		return TrustChanged, nil
	}
}

// Check is the verification result for one adapter directory.
type Check struct {
	Name  string
	State TrustState
	// Err is why the adapter could not be loaded: a malformed manifest, an
	// escaping path, a symbolic link, a name that does not match its directory.
	// State is empty when Err is set.
	Err error
}

// OK reports whether the adapter loaded and matches its recorded digest.
func (c Check) OK() bool { return c.Err == nil && c.State == TrustTrusted }

// Verify checks installed adapters without running any of them: it loads each
// one with the same validation an install applies, then compares its content
// with the recorded trust digest. With no names it checks every adapter
// directory under Home. Unlike List, it reports an adapter that fails to load
// instead of skipping it, so a damaged install is visible. An unreadable trust
// file is an error, since no adapter can then be called trusted. Results are
// sorted by name.
func (s *Store) Verify(names ...string) ([]Check, error) {
	if len(names) == 0 {
		entries, err := os.ReadDir(s.Home)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				names = append(names, e.Name())
			}
		}
	}
	trust, err := s.readTrust()
	if err != nil {
		return nil, fmt.Errorf("reading trust records: %w", err)
	}
	seen := map[string]bool{}
	checks := make([]Check, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		loaded, err := s.Load(name)
		if err != nil {
			checks = append(checks, Check{Name: name, Err: err})
			continue
		}
		state, err := trustStateOf(trust, loaded)
		if err != nil {
			checks = append(checks, Check{Name: name, Err: err})
			continue
		}
		checks = append(checks, Check{Name: name, State: state})
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	return checks, nil
}
