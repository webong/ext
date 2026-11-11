// Package storetest runs the shared store fixtures in
// res/graph/testdata/store/v1 against a store.Store implementation.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/res/graph/store"
)

// Backend opens the store under test.
type Backend struct {
	// Open opens a store in dir with the options. Opening the same dir again
	// reopens the same store; a store that is not durable ignores dir.
	Open func(t testing.TB, dir string, options store.Options) store.Store
	// Durable stores also run the cases that reopen the store.
	Durable bool
}

// Dir returns the fixture directory: $EXT_GRAPH_TESTDATA/store/v1, or
// testdata/store/v1 found by walking up from the working directory.
func Dir() (string, error) {
	if root := os.Getenv("EXT_GRAPH_TESTDATA"); root != "" {
		return filepath.Join(root, "store", "v1"), nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "testdata", "store", "v1")
		if _, err := os.Stat(filepath.Join(candidate, "cases.json")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("res/graph store fixtures not found; set EXT_GRAPH_TESTDATA")
		}
		dir = parent
	}
}

// Load reads one fixture file into v.
func Load(t testing.TB, name string, v any) {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

type caseFile struct {
	Version int       `json:"version"`
	Cases   []fixture `json:"cases"`
}

type fixture struct {
	Name    string        `json:"name"`
	Options store.Options `json:"options"`
	Durable bool          `json:"durable"`
	Steps   []step        `json:"steps"`
}

type step struct {
	Op          string            `json:"op"`
	Schema      store.Schema      `json:"schema"`
	Transaction store.Transaction `json:"transaction"`
	Namespace   string            `json:"namespace"`
	ID          string            `json:"id"`
	Query       json.RawMessage   `json:"query"`
	Start       string            `json:"start"`
	MaxDepth    int               `json:"max_depth"`
	MaxVertices int               `json:"max_vertices"`
	After       uint64            `json:"after"`
	Limit       int               `json:"limit"`
	Buffer      int               `json:"buffer"`
	Expect      json.RawMessage   `json:"expect"`
	Error       string            `json:"error"`
}

// Run runs every case in cases.json against b.
func Run(t *testing.T, b Backend) {
	var file caseFile
	Load(t, "cases.json", &file)
	if file.Version != 1 || len(file.Cases) == 0 {
		t.Fatalf("cases.json: version %d with %d cases", file.Version, len(file.Cases))
	}
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Durable && !b.Durable {
				t.Skip("durable case")
			}
			dir := t.TempDir()
			s := b.Open(t, dir, c.Options)
			defer func() { _ = s.Close() }()
			for i, st := range c.Steps {
				if st.Op == "reopen" {
					if err := s.Close(); err != nil {
						t.Fatalf("step %d: close: %v", i, err)
					}
					s = b.Open(t, dir, c.Options)
					continue
				}
				got, err := run(s, st)
				if code := store.Code(err); code != st.Error {
					t.Fatalf("step %d (%s): code %q, want %q (error: %v)", i, st.Op, code, st.Error, err)
				}
				if err != nil {
					if given := echoedInput(st, err); given != "" {
						t.Fatalf("step %d (%s): error text %q carries the given value %q", i, st.Op, err, given)
					}
				}
				if err != nil || st.Expect == nil {
					continue
				}
				var want any
				if err := json.Unmarshal(st.Expect, &want); err != nil {
					t.Fatalf("step %d: expect: %v", i, err)
				}
				actual, err := asJSON(got)
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if !Match(want, actual) {
					encoded, _ := json.Marshal(actual)
					t.Fatalf("step %d (%s): got %s, want %s", i, st.Op, encoded, st.Expect)
				}
			}
		})
	}
}

func run(s store.Store, st step) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch st.Op {
	case "register":
		return nil, s.Register(ctx, st.Schema)
	case "apply":
		return s.Apply(ctx, st.Transaction)
	case "get_vertex":
		return s.GetVertex(ctx, st.Namespace, st.ID)
	case "get_edge":
		return s.GetEdge(ctx, st.Namespace, st.ID)
	case "query_vertices":
		var q store.VertexQuery
		if err := json.Unmarshal(st.Query, &q); err != nil {
			return nil, err
		}
		return s.QueryVertices(ctx, q)
	case "query_edges":
		var q store.EdgeQuery
		if err := json.Unmarshal(st.Query, &q); err != nil {
			return nil, err
		}
		return s.QueryEdges(ctx, q)
	case "traverse":
		return s.Traverse(ctx, st.Namespace, st.Start, st.MaxDepth, st.MaxVertices)
	case "snapshot":
		return s.Snapshot(ctx, st.Namespace)
	case "changes":
		return s.Changes(ctx, st.Namespace, st.After, st.Limit)
	case "watch":
		var want []any
		if st.Expect != nil {
			if err := json.Unmarshal(st.Expect, &want); err != nil {
				return nil, err
			}
		}
		ch, err := s.Watch(ctx, st.Namespace, st.After, st.Buffer)
		if err != nil {
			return nil, err
		}
		got := []store.Change{}
		for len(got) < len(want) {
			select {
			case change, ok := <-ch:
				if !ok {
					return got, nil
				}
				got = append(got, change)
			case <-time.After(2 * time.Second):
				return got, nil
			}
		}
		return got, nil
	}
	return nil, fmt.Errorf("unknown step %q", st.Op)
}

func asJSON(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(data, &out)
}

// Match reports whether actual, a decoded JSON value, satisfies want, the
// expectation of a case. An object matches when each field it names matches,
// and a field absent from actual counts as null. A string in place of an
// object matches the object's "id". Two RFC 3339 strings match when they name
// the same instant. Null, false, 0, "" and [] match an absent value.
func Match(want, actual any) bool {
	switch w := want.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if actual == nil {
			a, ok = map[string]any{}, true
		}
		if !ok {
			return false
		}
		for key, value := range w {
			if !Match(value, a[key]) {
				return false
			}
		}
		return true
	case []any:
		if actual == nil {
			return len(w) == 0
		}
		a, ok := actual.([]any)
		if !ok || len(a) != len(w) {
			return false
		}
		for i := range w {
			if !Match(w[i], a[i]) {
				return false
			}
		}
		return true
	case string:
		switch a := actual.(type) {
		case string:
			if a == w {
				return true
			}
			x, err1 := time.Parse(time.RFC3339Nano, w)
			y, err2 := time.Parse(time.RFC3339Nano, a)
			return err1 == nil && err2 == nil && x.Equal(y)
		case map[string]any:
			return a["id"] == w
		case nil:
			return w == ""
		}
		return false
	case nil:
		return actual == nil
	case bool:
		if actual == nil {
			return !w
		}
		return actual == w
	case float64:
		if actual == nil {
			return w == 0
		}
		return actual == w
	}
	return reflect.DeepEqual(want, actual)
}

type fingerprintFile struct {
	Version int `json:"version"`
	Cases   []struct {
		Name        string          `json:"name"`
		Transaction json.RawMessage `json:"transaction"`
		Canonical   string          `json:"canonical"`
		SHA256      string          `json:"sha256"`
	} `json:"cases"`
}

// RunFingerprints checks store.Canonical and store.Fingerprint against
// fingerprints.json.
func RunFingerprints(t *testing.T) {
	var file fingerprintFile
	Load(t, "fingerprints.json", &file)
	if len(file.Cases) == 0 {
		t.Fatal("fingerprints.json has no cases")
	}
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var tx store.Transaction
			if err := json.Unmarshal(c.Transaction, &tx); err != nil {
				t.Fatal(err)
			}
			canonical, err := store.Canonical(tx)
			if err != nil {
				t.Fatal(err)
			}
			if string(canonical) != c.Canonical {
				t.Fatalf("canonical\n got %s\nwant %s", canonical, c.Canonical)
			}
			sum, err := store.Fingerprint(tx)
			if err != nil || sum != c.SHA256 {
				t.Fatalf("fingerprint %s, %v; want %s", sum, err, c.SHA256)
			}
		})
	}
}

// echoedInput returns a string the step was given that appears in err's text,
// or "". Error text never carries a value it was given (ADR rule 3). Strings
// shorter than four bytes are skipped, since they match ordinary words.
func echoedInput(st step, err error) string {
	in := st
	in.Op, in.Error, in.Expect = "", "", nil
	encoded, mErr := json.Marshal(in)
	if mErr != nil {
		return ""
	}
	var tree any
	if json.Unmarshal(encoded, &tree) != nil {
		return ""
	}
	text := err.Error()
	var found string
	check := func(v string) {
		if found == "" && len(v) >= 4 && strings.Contains(text, v) {
			found = v
		}
	}
	// Field names are the wire vocabulary, not input; the keys of attribute
	// and label maps are input.
	var walk func(v any, data bool)
	walk = func(v any, data bool) {
		switch v := v.(type) {
		case string:
			check(v)
		case []any:
			for _, e := range v {
				walk(e, false)
			}
		case map[string]any:
			for k, e := range v {
				if data {
					check(k)
				}
				walk(e, k == "attributes" || k == "labels")
			}
		}
	}
	walk(tree, false)
	return found
}
