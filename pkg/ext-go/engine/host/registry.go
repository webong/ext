package host

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// ErrExists is returned by a Registry for a contract that is already
// registered, or a reverse operation that is already routed.
var ErrExists = errors.New("already registered")

// Registry composes plugins through their host: a reverse call one plugin makes
// is served by a forward call to another plugin, along an edge the host
// declared with Route. The host stays the authority. Nothing changes on the
// wire: the calling plugin gets an ordinary reverse response and the target an
// ordinary call, and the registry routes only between processes the host
// opened; it discovers and starts nothing. It is the C engine's ext_registry
// (ext_host.h), and both answer the shared cases in
// lib/plugin/testdata/v1/registry-cases.json.
//
// A process is a source when it is opened with Options.Registry: its reverse
// calls are served by the registry, after Options.Reverse.Authorize allowed
// them. A process is a target once Register names the contract it serves. A
// reverse call the registry serves is answered with a RemoteError whose code is
//
//	reverse_undeclared   no edge leaves its (contract, operation)
//	reverse_unavailable  the edge's target contract is not registered, or its
//	                     process is closed, draining or does not offer the
//	                     operation
//	denied               the target's Options.Authorize refused the forward
//	                     call, or the call would re-enter a process already on
//	                     its call chain
//	expired              the forward call ran out of time
//
// and otherwise with the target's answer, unchanged: its payload, or its
// RemoteError. Any other failure is operation_failed. The forward call runs in
// the reverse call's context, so its deadline, cancellation and values reach the
// target.
//
// Cycles: a call chain starts at a source's reverse call and grows by one
// process for each forward call the registry makes. A reverse call a target
// makes inside such a forward call continues that chain; a session-scoped one
// starts a new chain at its source. An edge whose target is already on the chain
// (A to A, or A to B to A) is refused with denied instead of waiting on a
// process that is busy with the call that leads to it. A chain that leaves the
// registry (a Handle of the host's own that calls another plugin) is not seen
// and is bounded only by the deadline it carries.
//
// A Registry is safe for concurrent use, including while calls are routed.
type Registry struct {
	mu      sync.Mutex
	idle    *sync.Cond
	targets map[plugin.ContractRef]*target
	edges   map[edgeKey]edgeKey
}

type target struct {
	p    *Process
	busy int
	stop chan struct{} // closed when the contract is unregistered
}

type edgeKey struct {
	contract  plugin.ContractRef
	operation string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	r := &Registry{targets: map[plugin.ContractRef]*target{}, edges: map[edgeKey]edgeKey{}}
	r.idle = sync.NewCond(&r.mu)
	return r
}

// validOperation applies the descriptor's rule for operation names, which is
// the rule for contract names.
func validOperation(name string) bool {
	return plugin.ContractRef{Name: name, Version: "v1"}.Validate() == nil
}

// Register names p as the process that serves contract. p's descriptor must
// list the contract (ErrUnsupported otherwise); ErrExists when the contract is
// already registered. The contract is unregistered when p's session ends.
func (r *Registry) Register(contract plugin.ContractRef, p *Process) error {
	if p == nil || p.session == nil || contract.Validate() != nil {
		return plugin.ErrInvalid
	}
	serves := false
	for _, c := range p.Descriptor().Contracts {
		serves = serves || c.ContractRef == contract
	}
	if !serves {
		return plugin.ErrUnsupported
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.targets[contract] != nil {
		return ErrExists
	}
	t := &target{p: p, stop: make(chan struct{})}
	r.targets[contract] = t
	go func() {
		select {
		case <-p.Done():
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
func (r *Registry) remove(contract plugin.ContractRef, t *target) {
	delete(r.targets, contract)
	close(t.stop)
	for t.busy > 0 {
		r.idle.Wait()
	}
}

// Unregister removes contract and waits for the forward calls routed to its
// process to return. ErrNotFound when it is not registered.
func (r *Registry) Unregister(contract plugin.ContractRef) error {
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
// a call of toOperation in to. ErrExists when that reverse operation is already
// routed.
func (r *Registry) Route(from plugin.ContractRef, fromOperation string, to plugin.ContractRef, toOperation string) error {
	if from.Validate() != nil || to.Validate() != nil || !validOperation(fromOperation) || !validOperation(toOperation) {
		return plugin.ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := edgeKey{from, fromOperation}
	if _, ok := r.edges[key]; ok {
		return ErrExists
	}
	r.edges[key] = edgeKey{to, toOperation}
	return nil
}

// reverseOptions is the source's options with the registry as the handler.
func (r *Registry) reverseOptions(source *Process, o *ReverseOptions) (*ReverseOptions, error) {
	if o == nil || o.Authorize == nil {
		return nil, plugin.ErrDenied
	}
	if o.Handle != nil {
		return nil, plugin.ErrInvalid
	}
	served := *o
	served.Handle = r.serve(source)
	return &served, nil
}

// link is one process on a call chain.
type link struct {
	p  *Process
	up *link
}

type chainKey struct{ r *Registry }

func refused(code string) error {
	return &plugin.RemoteError{Code: code, Message: "reverse call refused"}
}

func (r *Registry) serve(source *Process) plugin.Handler {
	return func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
		// A call-scoped request inside a forward call this registry made carries on
		// that call's chain; any other starts one at its source.
		chain, _ := ctx.Value(chainKey{r}).(*link)
		if request.Parent == "" || chain == nil || chain.p != source {
			chain = &link{p: source}
		}
		r.mu.Lock()
		to, ok := r.edges[edgeKey{request.Contract, request.Operation}]
		var t *target
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
				if l.p == t.p {
					code = plugin.ReverseDenied
				}
			}
		}
		if code != "" {
			r.mu.Unlock()
			return nil, refused(code)
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
		forward := context.WithValue(ctx, chainKey{r}, &link{p: t.p, up: chain})
		payload, err := t.p.session.Call(forward, to.contract, to.operation, request.Payload)
		var remote *plugin.RemoteError
		switch {
		case err == nil:
			return payload, nil
		case errors.As(err, &remote):
			return nil, remote
		case errors.Is(err, plugin.ErrDenied):
			return nil, refused(plugin.ReverseDenied)
		case errors.Is(err, plugin.ErrClosed), errors.Is(err, plugin.ErrDraining), errors.Is(err, plugin.ErrUnsupported):
			return nil, refused(plugin.ReverseUnavailable)
		case errors.Is(err, context.DeadlineExceeded):
			return nil, refused(plugin.ReverseExpired)
		}
		return nil, err
	}
}
