// This consumer-owned example uses Xallet-shaped names to demonstrate that CTX
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
			if edge.Type == "xallet.io/observes" {
				from, _ := view.Vertex("xallet.io", edge.From)
				to, _ := view.Vertex("xallet.io", edge.To)
				if from.Kind != "xallet.io/organizer" || to.Kind != "xallet.io/packet" {
					return fmt.Errorf("xallet.io/observes requires an organizer and packet")
				}
			}
		}
		return nil
	}
	if err := store.Register(graph.Schema{Namespace: "xallet.io", Version: "1", Validate: validate}); err != nil {
		panic(err)
	}
	_, err := store.Apply(context.Background(), graph.Transaction{Namespace: "xallet.io", Vertices: []graph.Vertex{
		{ID: "organizer/primary", Kind: "xallet.io/organizer"},
		{ID: "packet/network", Kind: "xallet.io/packet"},
	}, Edges: []graph.Edge{{ID: "observes/network", From: "organizer/primary", To: "packet/network", Type: "xallet.io/observes"}}})
	if err != nil {
		panic(err)
	}
	snapshot, err := store.Snapshot(context.Background(), "xallet.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("consumer namespace %s has %d vertices and %d edges\n", snapshot.Namespace, len(snapshot.Vertices), len(snapshot.Edges))
}
