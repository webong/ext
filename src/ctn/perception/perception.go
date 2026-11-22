// Package perception binds the portable intelligence.perception/v1 contract to
// Ext plugin sessions. Its wire payloads are defined by res/int/perception.
package perception

import (
	"context"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	domain "github.com/webong/ext/res/int/perception"
)

const ContractName = "intelligence.perception"
const ContractVersion = "v1"
const Protocol = ContractName + "/" + ContractVersion

var ContractRef = plugin.ContractRef{Name: ContractName, Version: ContractVersion}

// Descriptor is the stable provider surface used by process hosts.
func Descriptor(identity plugin.Identity) plugin.Descriptor {
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   identity,
		Contracts: []plugin.Contract{{ContractRef: ContractRef, Operations: []plugin.Operation{
			{Name: "capabilities", Surface: "observation"},
			{Name: "observe", Surface: "action"},
			{Name: "interpret", Surface: "action"},
			{Name: "close", Surface: "action"},
		}}},
	}
}

func infoMethod() author.Method[struct{}, domain.Info] {
	return author.Method[struct{}, domain.Info]{Contract: ContractRef, Operation: plugin.Operation{Name: "capabilities", Surface: "observation"}, ValidateOutput: domain.Info.Validate}
}

func observeMethod() author.Method[domain.ObserveRequest, domain.ObserveResponse] {
	return author.Method[domain.ObserveRequest, domain.ObserveResponse]{Contract: ContractRef, Operation: plugin.Operation{Name: "observe", Surface: "action"}, ValidateInput: domain.ObserveRequest.Validate}
}

func interpretMethod() author.Method[domain.InterpretRequest, domain.InterpretResponse] {
	return author.Method[domain.InterpretRequest, domain.InterpretResponse]{Contract: ContractRef, Operation: plugin.Operation{Name: "interpret", Surface: "action"}, ValidateInput: domain.InterpretRequest.Validate}
}

func closeMethod() author.Method[struct{}, struct{}] {
	return author.Method[struct{}, struct{}]{Contract: ContractRef, Operation: plugin.Operation{Name: "close", Surface: "action"}}
}

// RegisterProvider declares the stable surface. An unsupported optional
// operation returns a typed remote error. Authorize is required on every call.
func RegisterProvider(registry *author.Registry, provider domain.Provider, authorize func(context.Context, plugin.Request) error) (*plugin.Descriptor, error) {
	if registry == nil || provider == nil || authorize == nil {
		return nil, plugin.ErrInvalid
	}
	if err := author.Register(registry, infoMethod(), func(ctx context.Context, _ plugin.Request, _ struct{}) (domain.Info, error) {
		return provider.Info(ctx)
	}); err != nil {
		return nil, err
	}
	if err := author.Register(registry, observeMethod(), func(ctx context.Context, _ plugin.Request, request domain.ObserveRequest) (domain.ObserveResponse, error) {
		observer, ok := provider.(domain.Observer)
		if !ok {
			return domain.ObserveResponse{}, &plugin.RemoteError{Code: "unsupported", Message: "provider does not support observation"}
		}
		response, err := observer.Observe(ctx, request)
		if err == nil {
			err = response.Validate(request)
		}
		return response, err
	}); err != nil {
		return nil, err
	}
	if err := author.Register(registry, interpretMethod(), func(ctx context.Context, _ plugin.Request, request domain.InterpretRequest) (domain.InterpretResponse, error) {
		interpreter, ok := provider.(domain.Interpreter)
		if !ok {
			return domain.InterpretResponse{}, &plugin.RemoteError{Code: "unsupported", Message: "provider does not support interpretation"}
		}
		response, err := interpreter.Interpret(ctx, request)
		if err == nil {
			err = response.Validate(request)
		}
		return response, err
	}); err != nil {
		return nil, err
	}
	if err := author.Register(registry, closeMethod(), func(_ context.Context, _ plugin.Request, _ struct{}) (struct{}, error) {
		return struct{}{}, provider.Close()
	}); err != nil {
		return nil, err
	}
	descriptor := registry.Descriptor()
	return &descriptor, nil
}

// Client invokes a provider through an already authorized plugin session.
type Client struct{ caller author.Caller }

func NewClient(caller author.Caller) *Client { return &Client{caller: caller} }

func (c *Client) Info(ctx context.Context) (domain.Info, error) {
	if c == nil || c.caller == nil {
		return domain.Info{}, plugin.ErrInvalid
	}
	return author.Call(ctx, c.caller, infoMethod(), struct{}{})
}

func (c *Client) Observe(ctx context.Context, request domain.ObserveRequest) (domain.ObserveResponse, error) {
	if c == nil || c.caller == nil {
		return domain.ObserveResponse{}, plugin.ErrInvalid
	}
	response, err := author.Call(ctx, c.caller, observeMethod(), request)
	if err == nil {
		err = response.Validate(request)
	}
	return response, err
}

func (c *Client) Interpret(ctx context.Context, request domain.InterpretRequest) (domain.InterpretResponse, error) {
	if c == nil || c.caller == nil {
		return domain.InterpretResponse{}, plugin.ErrInvalid
	}
	response, err := author.Call(ctx, c.caller, interpretMethod(), request)
	if err == nil {
		err = response.Validate(request)
	}
	return response, err
}

func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.caller == nil {
		return plugin.ErrInvalid
	}
	_, err := author.Call(ctx, c.caller, closeMethod(), struct{}{})
	return err
}
