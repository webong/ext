package crosslang_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

// The same JSON crosses both C interfaces. No Go graph implementation or
// product-specific translation participates in this test.
func TestExtGraphAcrossLibraries(t *testing.T) {
	open := func(variable, prefix string) *library.Described {
		t.Helper()
		path := os.Getenv(variable)
		if path == "" {
			t.Skip("requires " + variable)
		}
		native, err := library.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		api, err := library.BindInterface(native, prefix, 1)
		if err != nil {
			t.Fatal(err)
		}
		described, err := library.Describe(api)
		if err != nil {
			t.Fatal(err)
		}
		return described
	}
	plugin, ledger := open("EXT_PLUGIN_LIBRARY", "ext_plugin"), open("EXT_LEDGER_LIBRARY", "ext_ledger")
	data, err := os.ReadFile(filepath.Join("..", "..", "lib", "plugin", "testdata", "v1", "graph-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name, Operation, Status string
			Input                   json.RawMessage
			Result                  struct{ Schema, Transaction json.RawMessage }
		}
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Cases) == 0 {
		t.Fatal("no graph cases")
	}
	for _, c := range fixtures.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var projection struct{ Schema, Transaction json.RawMessage }
			err := plugin.Library().Call(c.Operation, c.Input, &projection)
			if c.Status != "ok" {
				var code *library.CodeError
				if !errors.As(err, &code) || code.Name != c.Status {
					t.Fatalf("want %s: %v", c.Status, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			equal := func(a, b json.RawMessage) bool {
				var x, y any
				if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
					return false
				}
				return reflect.DeepEqual(x, y)
			}
			if !equal(projection.Schema, c.Result.Schema) || !equal(projection.Transaction, c.Result.Transaction) {
				t.Fatal("C projection differs from shared fixture")
			}
			store, err := ledger.Open("store", map[string]any{"backend": "memory"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Call("register", map[string]any{"schema": projection.Schema}, nil); err != nil {
				t.Fatal(err)
			}
			if err := store.Call("apply", map[string]any{"transaction": projection.Transaction}, nil); err != nil {
				t.Fatal(err)
			}
			var tx struct {
				Namespace       string
				Vertices, Edges []map[string]json.RawMessage
			}
			if err := json.Unmarshal(projection.Transaction, &tx); err != nil {
				t.Fatal(err)
			}
			for _, group := range []struct {
				op      string
				records []map[string]json.RawMessage
			}{{"get_vertex", tx.Vertices}, {"get_edge", tx.Edges}} {
				for _, record := range group.records {
					var id string
					if err := json.Unmarshal(record["id"], &id); err != nil {
						t.Fatal(err)
					}
					var stored map[string]json.RawMessage
					if err := store.Call(group.op, map[string]any{"namespace": tx.Namespace, "id": id}, &stored); err != nil {
						t.Fatal(err)
					}
					for key, want := range record {
						if !equal(stored[key], want) {
							t.Fatalf("%s %s changed %s: %s", group.op, id, key, stored[key])
						}
					}
				}
			}
		})
	}
}
