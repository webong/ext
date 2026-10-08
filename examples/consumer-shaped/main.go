// This consumer-owned example uses consumer-shaped names to demonstrate that CTX
// accepts arbitrary namespaces and leaves semantic validation to the consumer.
package main

import (
	"context"
	"fmt"

	"github.com/webong/ext/pkg/graph"
)

func main() {
	store := graph.NewMemory()
	defer store.Close()
	validate := func(view graph.View, tx graph.Transaction) error {
		for _, edge := range tx.Edges {
			if edge.Type == "example.io/observes" {
				from, _ := view.Vertex("example.io", edge.From)
				to, _ := view.Vertex("example.io", edge.To)
				if from.Kind != "example.io/coordinator" || to.Kind != "example.io/resource" {
					return fmt.Errorf("example.io/observes requires an coordinator and resource")
				}
			}
		}
		return nil
	}
	if err := store.Register(graph.Schema{Namespace: "example.io", Version: "1", Validate: validate}); err != nil {
		panic(err)
	}
	_, err := store.Apply(context.Background(), graph.Transaction{Namespace: "example.io", Vertices: []graph.Vertex{
		{ID: "coordinator/primary", Kind: "example.io/coordinator"},
		{ID: "resource/network", Kind: "example.io/resource"},
	}, Edges: []graph.Edge{{ID: "observes/network", From: "coordinator/primary", To: "resource/network", Type: "example.io/observes"}}})
	if err != nil {
		panic(err)
	}
	snapshot, err := store.Snapshot(context.Background(), "example.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("consumer namespace %s has %d vertices and %d edges\n", snapshot.Namespace, len(snapshot.Vertices), len(snapshot.Edges))
}
