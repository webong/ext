package inference_test

import (
	"context"
	"errors"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	bridge "github.com/webong/ext/pkg/ext-go/inference"
	domain "github.com/webong/ext/res/int/inference"
	"testing"
)

type fake struct{}

func (fake) Info(context.Context) (domain.ProviderInfo, error) {
	return domain.ProviderInfo{ID: "test", Name: "Test", Version: "1"}, nil
}
func (fake) Close() error { return nil }
func (fake) Generate(context.Context, domain.GenerationRequest) (domain.GenerationResponse, error) {
	return domain.GenerationResponse{}, domain.NewRemoteError(domain.ErrAuthentication, "authentication failed", "test")
}
func (fake) Stream(ctx context.Context, _ domain.GenerationRequest, sink domain.EventSink) (domain.GenerationResponse, error) {
	return domain.GenerationResponse{}, sink.Emit(ctx, domain.GenerationEvent{Kind: domain.EventKindTextDelta, Text: "hello"})
}
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	auth := func(context.Context, plugin.Request) error { return nil }
	r, e := author.New(plugin.Identity{ID: "test", Revision: "r1", Version: "1"})
	if e != nil {
		t.Fatal(e)
	}
	d, e := bridge.RegisterProvider(r, fake{}, auth)
	if e != nil {
		t.Fatal(e)
	}
	g, e := r.Guest(author.Options{Authorize: auth})
	if e != nil {
		t.Fatal(e)
	}
	b, e := inprocess.New(g)
	if e != nil {
		t.Fatal(e)
	}
	s, e := plugin.Open(ctx, *d, plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }, Authorize: auth})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(ctx)
	c := bridge.NewClient(s)
	_, e = c.Generate(ctx, domain.GenerationRequest{})
	if !errors.Is(e, domain.ErrAuthentication) {
		t.Fatalf("lost error: %v", e)
	}
	stream, e := c.Stream(ctx, domain.GenerationRequest{})
	if e != nil {
		t.Fatal(e)
	}
	batch, e := stream.Read(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if len(batch.Items) != 1 || !batch.Done {
		t.Fatalf("bad batch: %+v", batch)
	}
	if e = stream.Close(ctx); e != nil {
		t.Fatal(e)
	}
}
