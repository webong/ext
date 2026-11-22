//go:build ext_cengine && cgo && (darwin || linux || windows)

package plugin

/*
#include "ext_go_bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// GuestRegistry routes reverse calls between sessions through the C engine's
// ext_registry.
type GuestRegistry struct {
	mu      sync.Mutex
	ptr     *C.ext_registry
	targets map[plugin.ContractRef]*Host
	// live counts the C hosts attached to or registered with ptr that are not yet
	// destroyed; the C registry may be freed only when it is zero.
	live int
}

// NewGuestRegistry returns an empty registry.
func NewGuestRegistry() *GuestRegistry {
	r := &GuestRegistry{targets: map[plugin.ContractRef]*Host{}}
	if C.ext_registry_create(&r.ptr) != C.EXT_OK {
		panic("cgoengine: cannot allocate a registry")
	}
	runtime.SetFinalizer(r, (*GuestRegistry).free)
	return r
}

// free releases the C registry once nothing uses it. A registry dropped while a
// session it serves was never closed is kept, since that session's engine
// threads may still call into it.
func (r *GuestRegistry) free() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == 0 && r.ptr != nil {
		C.ext_registry_destroy(r.ptr)
		r.ptr = nil
	}
}

func registryStatus(s C.ext_status) error {
	if s == C.EXT_EXISTS {
		return ErrRegistryExists
	}
	return status(s)
}

// cstrings copies the strings into C memory; free releases them.
func cstrings(values ...string) (out []*C.char, free func()) {
	for _, v := range values {
		out = append(out, C.CString(v))
	}
	return out, func() {
		for _, p := range out {
			C.free(unsafe.Pointer(p))
		}
	}
}

func validName(s string) bool {
	return plugin.ContractRef{Name: s, Version: "v1"}.Validate() == nil
}

// Register names s as the session that serves contract. s's descriptor must
// list the contract (ErrUnsupported otherwise); ErrRegistryExists when the
// contract is already registered.
func (r *GuestRegistry) Register(contract plugin.ContractRef, s *Session) error {
	if s == nil || s.Host == nil || contract.Validate() != nil {
		return plugin.ErrInvalid
	}
	h := s.Host
	names, free := cstrings(contract.Name, contract.Version)
	defer free()
	r.mu.Lock()
	h.mu.RLock()
	if h.ptr == nil {
		h.mu.RUnlock()
		r.mu.Unlock()
		return plugin.ErrClosed
	}
	err := registryStatus(C.ext_registry_register(r.ptr, names[0], names[1], h.ptr))
	h.mu.RUnlock()
	if err == nil {
		r.targets[contract] = h
		r.live++
	}
	r.mu.Unlock()
	if err == nil {
		h.onDestroy(func() {
			r.mu.Lock()
			if r.targets[contract] == h {
				r.remove(contract)
			}
			r.live--
			r.mu.Unlock()
		})
	}
	return err
}

// remove unregisters contract in C, which waits for the calls routed to it; the
// caller holds r.mu.
func (r *GuestRegistry) remove(contract plugin.ContractRef) {
	names, free := cstrings(contract.Name, contract.Version)
	defer free()
	delete(r.targets, contract)
	C.ext_registry_unregister(r.ptr, names[0], names[1])
}

// Unregister removes contract and waits for the forward calls routed to its
// session to return. ErrNotFound when it is not registered.
func (r *GuestRegistry) Unregister(contract plugin.ContractRef) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.targets[contract] == nil {
		return plugin.ErrNotFound
	}
	r.remove(contract)
	return nil
}

// Route declares one edge: a reverse call of fromOperation in from is served by
// a call of toOperation in to. ErrRegistryExists when that reverse operation is
// already routed.
func (r *GuestRegistry) Route(from plugin.ContractRef, fromOperation string, to plugin.ContractRef, toOperation string) error {
	if from.Validate() != nil || to.Validate() != nil || !validName(fromOperation) || !validName(toOperation) {
		return plugin.ErrInvalid
	}
	names, free := cstrings(from.Name, from.Version, fromOperation, to.Name, to.Version, toOperation)
	defer free()
	edge := C.ext_registry_edge{struct_size: C.uint32_t(unsafe.Sizeof(C.ext_registry_edge{})),
		from_name: names[0], from_version: names[1], from_operation: names[2],
		to_name: names[3], to_version: names[4], to_operation: names[5]}
	r.mu.Lock()
	defer r.mu.Unlock()
	return registryStatus(C.ext_registry_route(r.ptr, &edge))
}

// checkSource applies the rules for a source's reverse options: Authorize is
// required, the registry is the handler, and the bounds are in range.
func checkSource(o *plugin.ReverseOptions) error {
	if o == nil || o.Authorize == nil {
		return plugin.ErrDenied
	}
	if o.Handle != nil {
		return plugin.ErrInvalid
	}
	if o.Timeout < 0 || o.MaxPayloadBytes < 0 || o.MaxPayloadBytes > plugin.MaxFrameBytes || o.MaxInFlight < 0 || o.MaxInFlight > 1024 {
		return plugin.ErrInvalid
	}
	return nil
}

// attach makes h a source: its reverse calls are authorized by o.Authorize and
// served by the registry.
func (r *GuestRegistry) attach(ctx context.Context, h *Host, o *plugin.ReverseOptions) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := h.serveReverse(ctx, o, r.ptr); err != nil {
		return err
	}
	r.live++
	h.onDestroy(func() {
		r.mu.Lock()
		r.live--
		r.mu.Unlock()
	})
	return nil
}

// OpenProcess is OpenProcess with the plugin's reverse calls served by the
// registry. options.Reverse is required: its Authorize runs first and its bounds
// apply, and its Handle must be nil.
func (r *GuestRegistry) OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	if options.Verify == nil || options.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if options.Timeout < 0 || options.Timeout/time.Millisecond > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	if err := checkSource(options.Reverse); err != nil {
		return nil, err
	}
	return openSession(ctx, d, options, r, func() (*Host, error) {
		return NewLaunch(launch, d, func([]byte) error { return nil }, func([]byte) error { return nil })
	})
}
