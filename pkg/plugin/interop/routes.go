// Package interop plans explicit transport connections between CTX peers.
// It is metadata-only: it never discovers, launches, authenticates or retries.
package interop

import (
	"fmt"
	"github.com/webong/ext/pkg/plugin"
)

// Bridge declares an installed, reviewed translator. Frontend faces the host;
// Backend faces the guest. Its public features must not overstate either side.
type Bridge struct {
	Name     string                `json:"name"`
	Frontend plugin.BackendProfile `json:"frontend"`
	Backend  plugin.BackendProfile `json:"backend"`
}
type Requirements struct {
	Concurrent      bool
	NativeStreaming bool
	NativeCallbacks bool
}
type Route struct {
	Protocol        string                `json:"protocol"`
	Host            plugin.BackendProfile `json:"host"`
	Guest           plugin.BackendProfile `json:"guest"`
	Bridge          string                `json:"bridge,omitempty"`
	Concurrent      bool                  `json:"concurrent"`
	NativeStreaming bool                  `json:"nativeStreaming"`
	NativeCallbacks bool                  `json:"nativeCallbacks"`
}

func valid(p plugin.BackendProfile) error {
	if err := (plugin.Identity{ID: p.Name, Revision: "profile"}).Validate(); err != nil {
		return err
	}
	_, err := plugin.NegotiateProtocol(p.Protocols, p.Protocols)
	return err
}
func clone(p plugin.BackendProfile) plugin.BackendProfile {
	p.Protocols = append([]string(nil), p.Protocols...)
	return p
}
func meets(r Route, w Requirements) bool {
	return (!w.Concurrent || r.Concurrent) && (!w.NativeStreaming || r.NativeStreaming) && (!w.NativeCallbacks || r.NativeCallbacks)
}

// Resolve selects direct connections first, then a single declared bridge, in
// caller preference order. Names identify wire bindings, never source languages.
// Protocols must match end-to-end; this resolver does not translate versions.
// Requirements describe native transport features, not domain capabilities such
// as CTX pull-stream operations (which can run over unary transports).
func Resolve(hosts, guests []plugin.BackendProfile, bridges []Bridge, w Requirements) (Route, error) {
	if len(hosts) == 0 || len(guests) == 0 || len(hosts) > 64 || len(guests) > 64 || len(bridges) > 64 {
		return Route{}, plugin.ErrInvalid
	}
	for _, profiles := range [][]plugin.BackendProfile{hosts, guests} {
		seen := map[string]bool{}
		for _, p := range profiles {
			if err := valid(p); err != nil {
				return Route{}, err
			}
			if seen[p.Name] {
				return Route{}, plugin.ErrInvalid
			}
			seen[p.Name] = true
		}
	}
	seen := map[string]bool{}
	for _, b := range bridges {
		if err := (plugin.Identity{ID: b.Name, Revision: "bridge"}).Validate(); err != nil {
			return Route{}, err
		}
		if seen[b.Name] {
			return Route{}, plugin.ErrInvalid
		}
		seen[b.Name] = true
		if err := valid(b.Frontend); err != nil {
			return Route{}, err
		}
		if err := valid(b.Backend); err != nil {
			return Route{}, err
		}
	}
	for _, h := range hosts {
		for _, g := range guests {
			if h.Name != g.Name {
				continue
			}
			p, err := plugin.NegotiateProtocol(h.Protocols, g.Protocols)
			if err != nil {
				continue
			}
			r := Route{Protocol: p, Host: clone(h), Guest: clone(g), Concurrent: h.Concurrent && g.Concurrent, NativeStreaming: h.NativeStreaming && g.NativeStreaming, NativeCallbacks: h.NativeCallbacks && g.NativeCallbacks}
			if meets(r, w) {
				return r, nil
			}
		}
	}
	for _, h := range hosts {
		for _, g := range guests {
			for _, b := range bridges {
				if h.Name != b.Frontend.Name || g.Name != b.Backend.Name {
					continue
				}
				for _, p := range h.Protocols {
					ok := true
					for _, offered := range [][]string{g.Protocols, b.Frontend.Protocols, b.Backend.Protocols} {
						if _, err := plugin.NegotiateProtocol([]string{p}, offered); err != nil {
							ok = false
							break
						}
					}
					if !ok {
						continue
					}
					r := Route{Protocol: p, Host: clone(h), Guest: clone(g), Bridge: b.Name, Concurrent: h.Concurrent && g.Concurrent && b.Frontend.Concurrent && b.Backend.Concurrent, NativeStreaming: h.NativeStreaming && g.NativeStreaming && b.Frontend.NativeStreaming && b.Backend.NativeStreaming, NativeCallbacks: h.NativeCallbacks && g.NativeCallbacks && b.Frontend.NativeCallbacks && b.Backend.NativeCallbacks}
					if meets(r, w) {
						return r, nil
					}
				}
			}
		}
	}
	return Route{}, fmt.Errorf("%w: no declared route satisfies protocol and transport requirements", plugin.ErrUnsupported)
}
