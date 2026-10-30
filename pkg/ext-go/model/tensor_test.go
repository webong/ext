package model_test

import (
	"context"
	"errors"
	"os"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	bridge "github.com/webong/ext/pkg/ext-go/model"
	domain "github.com/webong/ext/res/int/model"
)

type unavailableProvider struct{}

func (unavailableProvider) Name() string           { return "example" }
func (unavailableProvider) Capabilities() []string { return nil }
func (unavailableProvider) Load(context.Context, *domain.InstalledModel) (domain.Session, error) {
	return nil, domain.ErrUnavailable
}

func TestTensorContractRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	auth := func(context.Context, plugin.Request) error { return nil }
	r, err := author.New(plugin.Identity{ID: "example", Revision: "r1", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := bridge.RegisterProvider(r, unavailableProvider{}, auth)
	if err != nil {
		t.Fatal(err)
	}
	g, err := r.Guest(author.Options{Authorize: auth})
	if err != nil {
		t.Fatal(err)
	}
	b, err := inprocess.New(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := plugin.Open(ctx, *d, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil },
		Authorize: auth,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	c := bridge.NewClient(s)
	_, err = c.Infer(ctx, bridge.InferRequest{Root: "/tmp", Reference: "example/m@v1"})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty tensors accepted: %v", err)
	}
}

func TestWireFixtures(t *testing.T) {
	requestBytes, err := os.ReadFile("testdata/v1/request.json")
	if err != nil {
		t.Fatal(err)
	}
	var request bridge.InferRequest
	if err := plugin.Decode(requestBytes, &request); err != nil {
		t.Fatal(err)
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	responseBytes, err := os.ReadFile("testdata/v1/response.json")
	if err != nil {
		t.Fatal(err)
	}
	var response bridge.InferResponse
	if err := plugin.Decode(responseBytes, &response); err != nil {
		t.Fatal(err)
	}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(response.Outputs) != 1 || len(response.Outputs[0].Float32()) != 2 || response.Outputs[0].Float32()[1] != 3 {
		t.Fatalf("fixture outputs: %+v", response.Outputs)
	}
}
