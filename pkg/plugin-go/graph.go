// Package plugingo is the umbrella of the Go plugin library: the module that
// requires every part at matching versions. The parts are modules of their own,
// so an application imports only what it needs:
//
//	github.com/webong/ext/pkg/plugin-go/contract   contract, sessions, packages, bridges (standard library only)
//	github.com/webong/ext/pkg/plugin-go/cengine    the C engine binding, with a pure-Go build of the same API
//	github.com/webong/ext/pkg/plugin-go/wasm       the WebAssembly (wazero) backend
//	github.com/webong/ext/pkg/plugin-go/hashicorp  the HashiCorp go-plugin (gRPC) backend
//
// What lives here, rather than in a part, is whatever needs more than one of
// them, such as the code that joins package plans to the host graph.
package plugingo

import (
	"fmt"

	"github.com/webong/ext/pkg/plugin-go/contract/packagekit"
	"github.com/webong/ext/res/graph"
)

// PlanGraph creates a graph transaction for the caller's namespace from a
// resolved plan. Apply it through the host's graph policy. Records express
// dependencies, never permissions.
func PlanGraph(p packagekit.Plan, namespace string) graph.Transaction {
	tx := graph.Transaction{Namespace: namespace}
	for _, id := range p.Order {
		tx.Vertices = append(tx.Vertices, graph.Vertex{Namespace: namespace, ID: packagekit.IdentityKey(id), Kind: "plugin", Attributes: map[string]any{"package": id.ID, "revision": id.Revision, "version": id.Version}})
	}
	for i, b := range p.Bindings {
		tx.Edges = append(tx.Edges, graph.Edge{Namespace: namespace, ID: fmt.Sprintf("requires-%s-%d", packagekit.IdentityKey(b.Consumer), i), From: packagekit.IdentityKey(b.Consumer), To: packagekit.IdentityKey(b.Provider), Type: "requires", Attributes: map[string]any{"contract": b.Requirement.Contract.Name, "version": b.Requirement.Contract.Version, "operation": b.Requirement.Operation}})
	}
	return tx
}
