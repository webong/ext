// Package author supplies typed operations and immutable guest construction.
// Domain authorization remains explicit on both sides of every connection.
package author

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/schema"
)

// Method is a shared typed contract. Schemas are optional, but recommended for
// cross-language contracts. ValidateInput/Output add domain validation.
type Method[Input, Output any] struct {
	Contract       plugin.ContractRef
	Operation      plugin.Operation
	Input          *schema.Schema
	Output         *schema.Schema
	ValidateInput  func(Input) error
	ValidateOutput func(Output) error
}

type key struct {
	contract  plugin.ContractRef
	operation string
}
type entry struct {
	op      plugin.Operation
	handler func(context.Context, plugin.Request, SchemaEngine) (json.RawMessage, error)
}

// SchemaEngine lets an embedding binding supply the shared schema mechanics.
// Implementations must match plugin/schema and be safe for concurrent calls.
type SchemaEngine interface {
	ValidateSchema(schema.Schema) error
	CheckSchema(schema.Schema, json.RawMessage) error
}
type nativeSchemas struct{}

func (nativeSchemas) ValidateSchema(s schema.Schema) error                    { return s.Validate() }
func (nativeSchemas) CheckSchema(s schema.Schema, data json.RawMessage) error { return s.Check(data) }

// Registry may be built concurrently. Guest snapshots it, so subsequent
// registration never expands a running guest's descriptor or dispatch table.
type Registry struct {
	mu       sync.Mutex
	identity plugin.Identity
	entries  map[key]entry
	schemas  SchemaEngine
}

func New(identity plugin.Identity) (*Registry, error) {
	return NewWithSchemaEngine(identity, nativeSchemas{})
}

func NewWithSchemaEngine(identity plugin.Identity, schemas SchemaEngine) (*Registry, error) {
	if schemas == nil {
		return nil, plugin.ErrInvalid
	}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	return &Registry{identity: identity, entries: map[key]entry{}, schemas: schemas}, nil
}

func Register[I, O any](r *Registry, method Method[I, O], handle func(context.Context, plugin.Request, I) (O, error)) error {
	if r == nil || handle == nil {
		return plugin.ErrInvalid
	}
	for _, s := range []*schema.Schema{method.Input, method.Output} {
		if s != nil {
			if err := r.schemas.ValidateSchema(*s); err != nil {
				return err
			}
		}
	}
	if method.Input != nil {
		c := method.Input.Clone()
		method.Input = &c
	}
	if method.Output != nil {
		c := method.Output.Clone()
		method.Output = &c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{method.Contract, method.Operation.Name}
	if _, ok := r.entries[k]; ok {
		return fmt.Errorf("%w: operation already registered", plugin.ErrInvalid)
	}
	handler := func(ctx context.Context, request plugin.Request, schemas SchemaEngine) (json.RawMessage, error) {
		var input I
		if err := decodeWithSchema(request.Payload, method.Input, &input, schemas); err != nil {
			return nil, &plugin.RemoteError{Code: "invalid_input", Message: "request payload does not satisfy operation contract"}
		}
		if method.ValidateInput != nil {
			if err := method.ValidateInput(input); err != nil {
				return nil, &plugin.RemoteError{Code: "invalid_input", Message: "request payload does not satisfy operation contract"}
			}
		}
		output, err := handle(ctx, request, input)
		if err != nil {
			return nil, err
		}
		if method.ValidateOutput != nil {
			if err := method.ValidateOutput(output); err != nil {
				return nil, fmt.Errorf("invalid handler output: %w", err)
			}
		}
		data, err := json.Marshal(output)
		if err != nil {
			return nil, err
		}
		if method.Output != nil {
			if err := schemas.CheckSchema(*method.Output, data); err != nil {
				return nil, err
			}
		}
		return data, nil
	}
	r.entries[k] = entry{method.Operation, handler}
	if err := r.descriptor().Validate(); err != nil {
		delete(r.entries, k)
		return err
	}
	return nil
}

func (r *Registry) descriptor() plugin.Descriptor {
	d := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: r.identity}
	grouped := map[plugin.ContractRef][]plugin.Operation{}
	for k, e := range r.entries {
		grouped[k.contract] = append(grouped[k.contract], e.op)
	}
	for ref, ops := range grouped {
		sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
		d.Contracts = append(d.Contracts, plugin.Contract{ContractRef: ref, Operations: ops})
	}
	sort.Slice(d.Contracts, func(i, j int) bool {
		a, b := d.Contracts[i], d.Contracts[j]
		if a.Name == b.Name {
			return a.Version < b.Version
		}
		return a.Name < b.Name
	})
	return d
}
func (r *Registry) Descriptor() plugin.Descriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.descriptor()
}

type Options struct {
	Authorize       func(context.Context, plugin.Request) error
	MaxCallDuration time.Duration
	// A binding may replace schema evaluation in this immutable guest snapshot.
	SchemaEngine SchemaEngine
}

func (r *Registry) Guest(opts Options) (*plugin.Guest, error) {
	d, options, err := r.Snapshot(opts)
	if err != nil {
		return nil, err
	}
	return plugin.NewGuest(d, options)
}

// Snapshot detaches the descriptor and dispatcher for construction by a host
// engine binding. Later registrations cannot expand the returned endpoint.
func (r *Registry) Snapshot(opts Options) (plugin.Descriptor, plugin.GuestOptions, error) {
	if opts.Authorize == nil {
		return plugin.Descriptor{}, plugin.GuestOptions{}, plugin.ErrDenied
	}
	r.mu.Lock()
	schemas := opts.SchemaEngine
	if schemas == nil {
		schemas = r.schemas
	}
	d := r.descriptor()
	handlers := make(map[key]entry, len(r.entries))
	for k, e := range r.entries {
		handlers[k] = e
	}
	r.mu.Unlock()
	return d, plugin.GuestOptions{MaxCallDuration: opts.MaxCallDuration, Handler: func(ctx context.Context, req plugin.Request) (json.RawMessage, error) {
		if err := opts.Authorize(ctx, req.Clone()); err != nil {
			return nil, &plugin.RemoteError{Code: "denied", Message: "operation denied"}
		}
		e, ok := handlers[key{req.Contract, req.Operation}]
		if !ok {
			return nil, plugin.ErrUnsupported
		}
		return e.handler(ctx, req, schemas)
	}}, nil
}

// Caller is implemented by Session and by explicit, policy-enforcing host
// service sessions supplied to guests. Request metadata never creates a caller.
type Caller interface {
	Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)
}

func Call[I, O any](ctx context.Context, caller Caller, method Method[I, O], input I) (O, error) {
	var output O
	if caller == nil {
		return output, plugin.ErrInvalid
	}
	var schemas SchemaEngine = nativeSchemas{}
	if engine, ok := caller.(SchemaEngine); ok {
		schemas = engine
	}
	if method.ValidateInput != nil {
		if err := method.ValidateInput(input); err != nil {
			return output, err
		}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return output, err
	}
	if method.Input != nil {
		if err := schemas.CheckSchema(*method.Input, data); err != nil {
			return output, err
		}
	}
	result, err := caller.Call(ctx, method.Contract, method.Operation.Name, data)
	if err != nil {
		return output, err
	}
	if err := decodeWithSchema(result, method.Output, &output, schemas); err != nil {
		return output, err
	}
	if method.ValidateOutput != nil {
		if err := method.ValidateOutput(output); err != nil {
			return output, err
		}
	}
	return output, nil
}
func decode(data json.RawMessage, s *schema.Schema, target any) error {
	return decodeWithSchema(data, s, target, nativeSchemas{})
}
func decodeWithSchema(data json.RawMessage, s *schema.Schema, target any, schemas SchemaEngine) error {
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	if s != nil {
		if err := schemas.CheckSchema(*s, data); err != nil {
			return err
		}
	}
	// Strongly typed payloads intentionally reject unknown fields as well as
	// duplicate keys. Version domain contracts when their accepted shape changes.
	return plugin.Decode(data, target)
}
