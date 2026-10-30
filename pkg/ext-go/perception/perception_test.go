package perception_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	bridge "github.com/webong/ext/pkg/ext-go/perception"
	domain "github.com/webong/ext/res/int/perception"
)

type fixtureProvider struct{}

func (fixtureProvider) Info(context.Context) (domain.Info, error) {
	return domain.Info{ID: "fixture", Capabilities: []string{"image.observe", "text.interpret"}}, nil
}

func TestLanguageNeutralFixtures(t *testing.T) {
	read := func(name string, value any) {
		t.Helper()
		data, err := os.ReadFile("testdata/v1/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, value); err != nil {
			t.Fatal(err)
		}
	}
	var interpret domain.InterpretRequest
	read("interpret-request", &interpret)
	if err := interpret.Validate(); err != nil {
		t.Fatal(err)
	}
	var interpretation domain.InterpretResponse
	read("interpret-response", &interpretation)
	if err := interpretation.Validate(interpret); err != nil {
		t.Fatal(err)
	}
	var observe domain.ObserveRequest
	read("observe-request", &observe)
	if err := observe.Validate(); err != nil {
		t.Fatal(err)
	}
	var observation domain.ObserveResponse
	read("observe-response", &observation)
	if err := observation.Validate(observe); err != nil {
		t.Fatal(err)
	}
}
func (fixtureProvider) Close() error { return nil }
func (fixtureProvider) Observe(_ context.Context, request domain.ObserveRequest) (domain.ObserveResponse, error) {
	return domain.ObserveResponse{APIVersion: domain.APIVersion, RequestID: request.RequestID, Observations: []domain.Observation{{Kind: "object", Label: "cat", Confidence: 0.9, Region: domain.Region{Width: 2, Height: 2}}}}, nil
}
func (fixtureProvider) Interpret(_ context.Context, request domain.InterpretRequest) (domain.InterpretResponse, error) {
	return domain.InterpretResponse{APIVersion: domain.APIVersion, RequestID: request.RequestID, Findings: []domain.Finding{{Kind: "summary", Text: "hello", SourceParts: []int{0}}}}, nil
}

func TestHostGuestRoundTrip(t *testing.T) {
	ctx := context.Background()
	authorize := func(context.Context, plugin.Request) error { return nil }
	registry, err := author.New(plugin.Identity{ID: "fixture", Revision: "r1", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := bridge.RegisterProvider(registry, fixtureProvider{}, authorize)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := registry.Guest(author.Options{Authorize: authorize})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := inprocess.New(guest)
	if err != nil {
		t.Fatal(err)
	}
	session, err := plugin.Open(ctx, *descriptor, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: authorize,
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(ctx)
	client := bridge.NewClient(session)
	request := domain.InterpretRequest{APIVersion: domain.APIVersion, RequestID: "r1", Model: "m", Parts: []domain.Part{{Kind: "text", Text: "hello"}}}
	response, err := client.Interpret(ctx, request)
	if err != nil || len(response.Findings) != 1 || response.Findings[0].SourceParts[0] != 0 {
		t.Fatalf("interpretation round trip: %+v, %v", response, err)
	}
	observation, err := client.Observe(ctx, domain.ObserveRequest{APIVersion: domain.APIVersion, RequestID: "r2", Model: "m", MediaType: "image/png", Image: []byte{1}})
	if err != nil || len(observation.Observations) != 1 || observation.Observations[0].Label != "cat" {
		t.Fatalf("observation round trip: %+v, %v", observation, err)
	}
}
