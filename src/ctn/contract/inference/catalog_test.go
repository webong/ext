package inference

import (
	"context"
	"testing"
)

type catalogProvider struct{}

func (catalogProvider) Info(context.Context) (ProviderInfo, error) { return ProviderInfo{ID: "p"}, nil }
func (catalogProvider) Close() error                               { return nil }

func TestModelCatalogCopiesMetadata(t *testing.T) {
	var catalog ModelCatalog
	info := ModelInfo{ID: "model", Provider: "p", Capabilities: []Capability{CapabilityGenerate}}
	if err := catalog.Register(info, catalogProvider{}); err != nil {
		t.Fatal(err)
	}
	info.Capabilities[0] = CapabilityEmbed
	registered, _, ok := catalog.Lookup("model")
	if !ok || registered.Capabilities[0] != CapabilityGenerate {
		t.Fatalf("registry leaked input mutation: %+v", registered)
	}
	registered.Capabilities[0] = CapabilityEmbed
	models := catalog.Models()
	if len(models) != 1 || models[0].Capabilities[0] != CapabilityGenerate {
		t.Fatalf("lookup leaked registry mutation: %+v", models)
	}
}
