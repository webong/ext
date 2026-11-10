package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/webong/ext/res/graph/store"
	"github.com/webong/ext/res/graph/store/storetest"
)

// Every code a case expects is in the shared vocabulary of res/system.
func TestCasesUseTheSharedCodes(t *testing.T) {
	dir, err := storetest.Dir()
	if err != nil {
		t.Fatal(err)
	}
	codesPath := filepath.Join(dir, "..", "..", "..", "..", "system", "testdata", "codes.json")
	data, err := os.ReadFile(codesPath)
	if err != nil {
		t.Skipf("res/system codes not beside this module: %v", err)
	}
	var vocabulary struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(data, &vocabulary); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, c := range vocabulary.Codes {
		known[c] = true
	}
	var cases struct {
		Cases []struct {
			Name  string `json:"name"`
			Steps []struct {
				Error string `json:"error"`
			} `json:"steps"`
		} `json:"cases"`
	}
	storetest.Load(t, "cases.json", &cases)
	for _, c := range cases.Cases {
		for i, s := range c.Steps {
			if s.Error != "" && !known[s.Error] {
				t.Errorf("%s step %d: code %q is not in codes.json", c.Name, i, s.Error)
			}
		}
	}
	var rules struct {
		Codes map[string]any `json:"codes"`
	}
	storetest.Load(t, "rules.json", &rules)
	for code := range rules.Codes {
		if code != "about" && !known[code] {
			t.Errorf("rules.json describes %q, which is not in codes.json", code)
		}
	}
}

// The Go wire form of each entity has exactly the fields entities.schema.json declares.
func TestEntitiesMatchTheSchema(t *testing.T) {
	var schema struct {
		Defs map[string]struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	storetest.Load(t, "entities.schema.json", &schema)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rev := uint64(1)
	p := store.Provenance{Source: "s", Operation: "o", At: at}
	v := store.Vertex{Namespace: "n", ID: "a", Kind: "n/k", Revision: 1, CreatedAt: at, UpdatedAt: at, Labels: map[string]string{"l": "v"}, Attributes: map[string]any{"a": 1}, Provenance: p}
	e := store.Edge{Namespace: "n", ID: "e", From: "a", To: "b", Type: "n/t", Revision: 1, CreatedAt: at, UpdatedAt: at, Labels: map[string]string{"l": "v"}, Attributes: map[string]any{"a": 1}, Provenance: p}
	entities := map[string]any{
		"provenance":   p,
		"vertex":       v,
		"edge":         e,
		"reference":    store.VertexRef{Namespace: "n", ID: "a"},
		"relationship": store.Relationship{Type: "n/t", From: "n/k", To: "n/k"},
		"schema":       store.Schema{Namespace: "n", Version: "1", Kinds: []string{"n/k"}, Relationships: []store.Relationship{{Type: "n/t"}}},
		"transaction":  store.Transaction{Namespace: "n", IdempotencyKey: "k", ExpectedRevision: &rev, Vertices: []store.Vertex{v}, Edges: []store.Edge{e}, DeleteVertices: []store.VertexRef{{ID: "a"}}, DeleteEdges: []store.EdgeRef{{ID: "e"}}, DeleteIncidentEdges: true},
		"commit":       store.Commit{Namespace: "n", Revision: 1, Cursor: 1, Committed: at, Replayed: true},
		"change":       store.Change{Cursor: 1, Namespace: "n", Revision: 1, Committed: at, Vertices: []store.Vertex{v}, Edges: []store.Edge{e}, DeletedVertices: []string{"x"}, DeletedEdges: []string{"y"}},
		"snapshot":     store.Snapshot{Namespace: "n", Revision: 1, Cursor: 1, HistoryFloor: 1, Vertices: []store.Vertex{v}, Edges: []store.Edge{e}},
		"path":         store.Path{Vertices: []store.Vertex{v}, Edges: []store.Edge{e}},
		"vertex_query": store.VertexQuery{Namespace: "n", Kind: "n/k", Limit: 1},
		"edge_query":   store.EdgeQuery{Namespace: "n", Type: "n/t", From: "a", To: "b", Limit: 1},
		"options":      store.Options{MaxChanges: 1, MaxChangeBytes: 1, MaxTransactionRecords: 1, MaxRecordBytes: 1, MaxResults: 1, MaxSnapshotRecords: 1},
	}
	for name, value := range entities {
		def, ok := schema.Defs[name]
		if !ok {
			t.Errorf("entities.schema.json has no %s", name)
			continue
		}
		data, _ := json.Marshal(value)
		var fields map[string]any
		_ = json.Unmarshal(data, &fields)
		var got, want []string
		for k := range fields {
			got = append(got, k)
		}
		for k := range def.Properties {
			want = append(want, k)
		}
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Errorf("%s: Go fields %v, schema properties %v", name, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: Go fields %v, schema properties %v", name, got, want)
				break
			}
		}
		for _, r := range def.Required {
			if _, ok := fields[r]; !ok {
				t.Errorf("%s: required %s missing from the Go form", name, r)
			}
		}
	}
}
