// Package ml bridges res/ml tensor providers to the Ext plugin contract.
// The wire contract is language-neutral; this package supplies Go host and
// guest helpers. A host still chooses and trusts a sealed provider package.
package ml

import (
	"context"
	"fmt"
	"path/filepath"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	domain "github.com/webong/ext/res/ml"
)

const ContractName = "model.tensor"
const ContractVersion = "v1"
const Protocol = ContractName + "/" + ContractVersion
const MaxWireTensorBytes = 16 << 20

var ContractRef = plugin.ContractRef{Name: ContractName, Version: ContractVersion}

type InferRequest struct {
	Root      string          `json:"root"`
	Reference string          `json:"reference"`
	Inputs    []domain.Tensor `json:"inputs"`
}

type InferResponse struct {
	Outputs []domain.Tensor `json:"outputs"`
}

func (r InferRequest) Validate() error {
	if r.Root == "" || !filepath.IsAbs(r.Root) {
		return fmt.Errorf("%w: an absolute model root is required", domain.ErrInvalid)
	}
	if _, err := domain.ReferenceFrom(r.Reference); err != nil {
		return err
	}
	if len(r.Inputs) == 0 || len(r.Inputs) > 64 {
		return fmt.Errorf("%w: expected 1 to 64 input tensors", domain.ErrInvalid)
	}
	total := 0
	for _, input := range r.Inputs {
		if err := input.Validate(); err != nil {
			return err
		}
		if len(input.Data) > MaxWireTensorBytes-total {
			return fmt.Errorf("%w: input tensors exceed the wire limit", domain.ErrInvalid)
		}
		total += len(input.Data)
	}
	return nil
}

func (r InferResponse) Validate() error {
	if len(r.Outputs) == 0 || len(r.Outputs) > 64 {
		return fmt.Errorf("%w: expected 1 to 64 output tensors", domain.ErrInvalid)
	}
	total := 0
	for _, output := range r.Outputs {
		if err := output.Validate(); err != nil {
			return err
		}
		if len(output.Data) > MaxWireTensorBytes-total {
			return fmt.Errorf("%w: output tensors exceed the wire limit", domain.ErrInvalid)
		}
		total += len(output.Data)
	}
	return nil
}

func inferMethod() author.Method[InferRequest, InferResponse] {
	return author.Method[InferRequest, InferResponse]{
		Contract:       ContractRef,
		Operation:      plugin.Operation{Name: "infer", Surface: "action"},
		ValidateInput:  InferRequest.Validate,
		ValidateOutput: InferResponse.Validate,
	}
}

// Client calls a model.tensor/v1 guest through an already authorized session.
type Client struct{ caller author.Caller }

func NewClient(caller author.Caller) *Client { return &Client{caller: caller} }

func (c *Client) Infer(ctx context.Context, request InferRequest) (InferResponse, error) {
	if err := request.Validate(); err != nil {
		return InferResponse{}, err
	}
	return author.Call(ctx, c.caller, inferMethod(), request)
}

// RegisterProvider serves a selected native provider. The guest reopens and
// verifies the model bundle on every call, and closes the native session.
// The authorize callback must enforce guest-side access policy.
func RegisterProvider(registry *author.Registry, provider domain.Provider, authorize func(context.Context, plugin.Request) error) (*plugin.Descriptor, error) {
	if registry == nil || provider == nil || authorize == nil {
		return nil, plugin.ErrInvalid
	}
	err := author.Register(registry, inferMethod(), func(ctx context.Context, _ plugin.Request, request InferRequest) (InferResponse, error) {
		ref, err := domain.ReferenceFrom(request.Reference)
		if err != nil {
			return InferResponse{}, err
		}
		if ref.Provider != provider.Name() {
			return InferResponse{}, fmt.Errorf("%w: model provider does not match guest", domain.ErrInvalid)
		}
		store, err := domain.OpenStore(request.Root)
		if err != nil {
			return InferResponse{}, err
		}
		defer store.Close()
		model, err := store.Resolve(ctx, ref.Provider, ref.Name, ref.Version)
		if err != nil {
			return InferResponse{}, err
		}
		session, err := provider.Load(ctx, model)
		if err != nil {
			return InferResponse{}, err
		}
		defer session.Close()
		outputs, err := session.Infer(ctx, request.Inputs)
		return InferResponse{Outputs: outputs}, err
	})
	if err != nil {
		return nil, err
	}
	descriptor := registry.Descriptor()
	return &descriptor, nil
}
