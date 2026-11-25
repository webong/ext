// This consumer-owned example uses consumer-shaped names to demonstrate that CTX
// accepts arbitrary namespaces and leaves the vocabulary to the consumer, which
// declares it as a schema.
package main

import (
	"context"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"os"

	store "github.com/webong/ext/pkg/ext-go/cengine/ledger"
)

func main() {
	ctx := context.Background()
	native, err := library.Open(os.Getenv("EXT_LEDGER_LIBRARY"))
	if err != nil {
		panic(err)
	}
	lib, err := store.Bind(native)
	if err != nil {
		panic(err)
	}
	memory, err := lib.OpenStore(ctx, "memory", "", store.Options{})
	if err != nil {
		panic(err)
	}
	defer memory.Close()
	schema := store.Schema{
		Namespace: "example.io",
		Version:   "1",
		Kinds:     []string{"example.io/coordinator", "example.io/resource"},
		Relationships: []store.Relationship{
			{Type: "example.io/observes", From: "example.io/coordinator", To: "example.io/resource"},
		},
	}
	if err := memory.Register(ctx, schema); err != nil {
		panic(err)
	}
	_, err = memory.Apply(ctx, store.Transaction{Namespace: "example.io", Vertices: []store.Vertex{
		{ID: "coordinator/primary", Kind: "example.io/coordinator"},
		{ID: "resource/network", Kind: "example.io/resource"},
	}, Edges: []store.Edge{{ID: "observes/network", From: "coordinator/primary", To: "resource/network", Type: "example.io/observes"}}})
	if err != nil {
		panic(err)
	}
	snapshot, err := memory.Snapshot(ctx, "example.io")
	if err != nil {
		panic(err)
	}
	fmt.Printf("consumer namespace %s has %d vertices and %d edges\n", snapshot.Namespace, len(snapshot.Vertices), len(snapshot.Edges))
}
