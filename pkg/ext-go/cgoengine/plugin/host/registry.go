package host

import (
	cgoengine "github.com/webong/ext/pkg/ext-go/cgoengine/plugin"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// ErrExists is returned by a Registry for a contract that is already
// registered, or a reverse operation that is already routed.
var ErrExists = cgoengine.ErrRegistryExists

// Registry composes plugins through their host: a reverse call one plugin makes
// is served by a forward call to another plugin, along an edge the host
// declared with Route. It is the C engine's ext_plugin_registry (ext_plugin_host.h) through
// cgoengine.GuestRegistry, and pkg/ext-go/engine/plugin/host's Registry documents the
// rules, the refusal codes and how cycles are refused; both answer
// lib/plugin/testdata/v1/registry-cases.json.
//
// A process is a source when it is opened with Options.Registry, and a target
// once Register names the contract it serves. The contract is unregistered when
// the target's session ends. A Registry is safe for concurrent use.
type Registry struct {
	r *cgoengine.GuestRegistry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{r: cgoengine.NewGuestRegistry()}
}

// Register names p as the process that serves contract. p's descriptor must
// list the contract (ErrUnsupported otherwise); ErrExists when the contract is
// already registered. The contract is unregistered when p's session ends.
func (r *Registry) Register(contract plugin.ContractRef, p *Process) error {
	if p == nil {
		return plugin.ErrInvalid
	}
	return r.r.Register(contract, p.session)
}

// Unregister removes contract and waits for the forward calls routed to its
// process to return. ErrNotFound when it is not registered.
func (r *Registry) Unregister(contract plugin.ContractRef) error {
	return r.r.Unregister(contract)
}

// Route declares one edge: a reverse call of fromOperation in from is served by
// a call of toOperation in to. ErrExists when that reverse operation is already
// routed.
func (r *Registry) Route(from plugin.ContractRef, fromOperation string, to plugin.ContractRef, toOperation string) error {
	return r.r.Route(from, fromOperation, to, toOperation)
}
