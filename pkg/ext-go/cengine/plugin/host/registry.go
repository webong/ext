package host

import (
	native "github.com/webong/ext/pkg/ext-go/cengine/plugin"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

// ErrExists is returned by a Registry for a contract that is already
// registered, or a reverse operation that is already routed.
var ErrExists = native.ErrRegistryExists

// Registry composes plugins through their host: a reverse call one plugin makes
// is served by a forward call to another plugin, along an edge the host
// declared with Route. It is the C engine's ext_plugin_registry (ext_plugin_host.h) through
// cengine/plugin.Registry. Its refusal codes and cycle rules are checked by
// lib/plugin/testdata/v1/registry-cases.json.
//
// A process is a source when it is opened with Options.Registry, and a target
// once Register names the contract it serves. The contract is unregistered when
// the target's session ends. A Registry is safe for concurrent use.
type Registry struct {
	r   *native.Registry
	err error
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	lib, err := native.Default()
	if err != nil {
		return &Registry{err: err}
	}
	r, err := lib.NewRegistry()
	return &Registry{r: r, err: err}
}

// Register names p as the process that serves contract. p's descriptor must
// list the contract (ErrUnsupported otherwise); ErrExists when the contract is
// already registered. The contract is unregistered when p's session ends.
func (r *Registry) Register(contract plugin.ContractRef, p *Process) error {
	if p == nil {
		return plugin.ErrInvalid
	}
	if r.err != nil {
		return r.err
	}
	return r.r.Register(contract, p.session)
}

// Unregister removes contract and waits for the forward calls routed to its
// process to return. ErrNotFound when it is not registered.
func (r *Registry) Unregister(contract plugin.ContractRef) error {
	if r.err != nil {
		return r.err
	}
	return r.r.Unregister(contract)
}

// Route declares one edge: a reverse call of fromOperation in from is served by
// a call of toOperation in to. ErrExists when that reverse operation is already
// routed.
func (r *Registry) Route(from plugin.ContractRef, fromOperation string, to plugin.ContractRef, toOperation string) error {
	if r.err != nil {
		return r.err
	}
	return r.r.Route(from, fromOperation, to, toOperation)
}
