package plugingo

import (
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/package"
	"testing"
)

func TestPlanGraph(t *testing.T) {
	desc := func(id string) plugin.Descriptor {
		return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: id, Revision: "r1"}, Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: id, Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}}
	}
	a, b := desc("example/a"), desc("example/b")
	p := packagekit.Plan{
		Order:    []plugin.Identity{b.Identity, a.Identity},
		Bindings: []packagekit.Resolution{{Consumer: a.Identity, Provider: b.Identity, Requirement: packagekit.Dependency{Contract: b.Contracts[0].ContractRef, Operation: "read"}}},
	}
	tx := PlanGraph(p, "plugins")
	if tx.Namespace != "plugins" || len(tx.Vertices) != 2 || len(tx.Edges) != 1 || tx.Edges[0].Type != "requires" {
		t.Fatal(tx)
	}
	if tx.Edges[0].From != packagekit.IdentityKey(a.Identity) || tx.Edges[0].To != packagekit.IdentityKey(b.Identity) {
		t.Fatal(tx.Edges[0])
	}
}
