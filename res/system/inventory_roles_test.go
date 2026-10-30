package system

import (
	"context"
	"reflect"
	"testing"
)

func TestResolveInventoryCarriesEngineAndRole(t *testing.T) {
	g, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapters := []AdapterObservation{{
		Name: "jvm", Runtime: "computer", Trusted: true, Listed: true,
		Supports: []string{"jvm"}, Engine: []string{"evaluator"}, Role: []string{"virtualizer", "compiler"},
		Contexts: []ContextObservation{{Selection: "21"}},
	}}
	if err := g.ObserveInventory(ctx, adapters, nil); err != nil {
		t.Fatal(err)
	}
	inventory, err := g.ResolveInventory(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Contexts) != 1 {
		t.Fatalf("contexts = %#v", inventory.Contexts)
	}
	got := inventory.Contexts[0]
	if !reflect.DeepEqual(got.Engine, []string{"evaluator"}) || !reflect.DeepEqual(got.Role, []string{"compiler", "virtualizer"}) || !reflect.DeepEqual(got.Supports, []string{"jvm"}) {
		t.Fatalf("engine = %v, role = %v, supports = %v", got.Engine, got.Role, got.Supports)
	}
}
