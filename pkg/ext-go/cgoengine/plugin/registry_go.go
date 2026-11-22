//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// GuestRegistry routes reverse calls between sessions; this build serves it in
// Go, with the behavior of the C engine's ext_registry.
type GuestRegistry struct {
	mu      sync.Mutex
	idle    *sync.Cond
	targets map[plugin.ContractRef]*guestTarget
	edges   map[guestEdge]guestEdge
}

type guestTarget struct {
	s    *Session
	busy int
	stop chan struct{}
}

type guestEdge struct {
	contract  plugin.ContractRef
	operation string
}

// NewGuestRegistry returns an empty registry.
func NewGuestRegistry() *GuestRegistry {
	r := &GuestRegistry{targets: map[plugin.ContractRef]*guestTarget{}, edges: map[guestEdge]guestEdge{}}
	r.idle = sync.NewCond(&r.mu)
	return r
}

func validName(s string) bool {
	return plugin.ContractRef{Name: s, Version: "v1"}.Validate() == nil
}

// Register names s as the session that serves contract. s's descriptor must
// list the contract (ErrUnsupported otherwise); ErrRegistryExists when the
// contract is already registered.
func (r *GuestRegistry) Register(contract plugin.ContractRef, s *Session) error {
	if s == nil || s.Session == nil || contract.Validate() != nil {
		return plugin.ErrInvalid
	}
	serves := false
	for _, c := range s.Descriptor().Contracts {
		serves = serves || c.ContractRef == contract
	}
	if !serves {
		return plugin.ErrUnsupported
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.targets[contract] != nil {
		return ErrRegistryExists
	}
	t := &guestTarget{s: s, stop: make(chan struct{})}
	r.targets[contract] = t
	go func() {
		select {
		case <-s.Done():
			r.mu.Lock()
			if r.targets[contract] == t {
				r.remove(contract, t)
			}
			r.mu.Unlock()
		case <-t.stop:
		}
	}()
	return nil
}

// remove unregisters t and waits for its calls; the caller holds r.mu.
func (r *GuestRegistry) remove(contract plugin.ContractRef, t *guestTarget) {
	delete(r.targets, contract)
	close(t.stop)
	for t.busy > 0 {
		r.idle.Wait()
	}
}

// Unregister removes contract and waits for the forward calls routed to its
// session to return. ErrNotFound when it is not registered.
func (r *GuestRegistry) Unregister(contract plugin.ContractRef) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.targets[contract]
	if t == nil {
		return plugin.ErrNotFound
	}
	r.remove(contract, t)
	return nil
}

// Route declares one edge: a reverse call of fromOperation in from is served by
// a call of toOperation in to. ErrRegistryExists when that reverse operation is
// already routed.
func (r *GuestRegistry) Route(from plugin.ContractRef, fromOperation string, to plugin.ContractRef, toOperation string) error {
	if from.Validate() != nil || to.Validate() != nil || !validName(fromOperation) || !validName(toOperation) {
		return plugin.ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := guestEdge{from, fromOperation}
	if _, ok := r.edges[key]; ok {
		return ErrRegistryExists
	}
	r.edges[key] = guestEdge{to, toOperation}
	return nil
}

// OpenProcess is OpenProcess with the plugin's reverse calls served by the
// registry. options.Reverse is required: its Authorize runs first and its bounds
// apply, and its Handle must be nil.
func (r *GuestRegistry) OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	o := options.Reverse
	if o == nil || o.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if o.Handle != nil {
		return nil, plugin.ErrInvalid
	}
	source := &Session{}
	served := *o
	served.Handle = r.serve(source)
	options.Reverse = &served
	return openProcess(ctx, d, launch, options, source)
}

type guestLink struct {
	s  *Session
	up *guestLink
}

type guestChainKey struct{ r *GuestRegistry }

func guestRefused(code string) error {
	return &plugin.RemoteError{Code: code, Message: "reverse call refused"}
}

func (r *GuestRegistry) serve(source *Session) plugin.Handler {
	return func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
		chain, _ := ctx.Value(guestChainKey{r}).(*guestLink)
		if request.Parent == "" || chain == nil || chain.s != source {
			chain = &guestLink{s: source}
		}
		r.mu.Lock()
		to, ok := r.edges[guestEdge{request.Contract, request.Operation}]
		var t *guestTarget
		if ok {
			t = r.targets[to.contract]
		}
		code := ""
		switch {
		case !ok:
			code = plugin.ReverseUndeclared
		case t == nil:
			code = plugin.ReverseUnavailable
		default:
			for l := chain; l != nil && code == ""; l = l.up {
				if l.s == t.s {
					code = plugin.ReverseDenied
				}
			}
		}
		if code != "" {
			r.mu.Unlock()
			return nil, guestRefused(code)
		}
		t.busy++
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			if t.busy--; t.busy == 0 {
				r.idle.Broadcast()
			}
			r.mu.Unlock()
		}()
		forward := context.WithValue(ctx, guestChainKey{r}, &guestLink{s: t.s, up: chain})
		payload, err := t.s.Call(forward, to.contract, to.operation, request.Payload)
		var remote *plugin.RemoteError
		switch {
		case err == nil:
			return payload, nil
		case errors.As(err, &remote):
			return nil, remote
		case errors.Is(err, plugin.ErrDenied):
			return nil, guestRefused(plugin.ReverseDenied)
		case errors.Is(err, plugin.ErrClosed), errors.Is(err, plugin.ErrDraining), errors.Is(err, plugin.ErrUnsupported):
			return nil, guestRefused(plugin.ReverseUnavailable)
		case errors.Is(err, context.DeadlineExceeded):
			return nil, guestRefused(plugin.ReverseExpired)
		}
		return nil, err
	}
}
