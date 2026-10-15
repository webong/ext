//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/packagekit"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"
)

type position []any

// positions lists every settable position of doc: object keys and array indexes.
func positions(doc any) []position {
	var out []position
	var walk func(node any, path position)
	walk = func(node any, path position) {
		switch n := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				p := append(append(position{}, path...), k)
				out = append(out, p)
				walk(n[k], p)
			}
		case []any:
			for i := range n {
				p := append(append(position{}, path...), i)
				out = append(out, p)
				walk(n[i], p)
			}
		}
	}
	walk(doc, nil)
	return out
}

func overlaps(a, b position) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true // one is a prefix of the other, or they are equal
}

// later reports whether a sorts after b, comparing array indexes numerically.
func later(a, b position) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		switch x := a[i].(type) {
		case int:
			if y := b[i].(int); x != y {
				return x > y
			}
		case string:
			if y := b[i].(string); x != y {
				return x > y
			}
		}
	}
	return len(a) > len(b)
}

func setAt(node any, path position, value any) any {
	if len(path) == 0 {
		return value
	}
	switch n := node.(type) {
	case map[string]any:
		key := path[0].(string)
		if len(path) == 1 && value == removeMarker {
			delete(n, key)
			return n
		}
		n[key] = setAt(n[key], path[1:], value)
		return n
	case []any:
		i := path[0].(int)
		if i >= len(n) {
			return n
		}
		if len(path) == 1 && value == removeMarker {
			return append(n[:i:i], n[i+1:]...)
		}
		n[i] = setAt(n[i], path[1:], value)
		return n
	}
	return node
}

// pairMutations returns count documents, each differing from doc at two
// independent positions. Sampling is seeded, so a failure reproduces.
func pairMutations(doc any, count int, seed int64) []json.RawMessage {
	rng := rand.New(rand.NewSource(seed))
	all := positions(doc)
	edges := append(edgeValues(), removeMarker)
	var out []json.RawMessage
	for len(out) < count {
		a, b := all[rng.Intn(len(all))], all[rng.Intn(len(all))]
		if overlaps(a, b) {
			continue
		}
		clone := deepCopy(doc)
		// Apply the later position first, so removing an array element cannot shift the other.
		if later(b, a) {
			a, b = b, a
		}
		clone = setAt(clone, a, edges[rng.Intn(len(edges))])
		clone = setAt(clone, b, edges[rng.Intn(len(edges))])
		raw, err := json.Marshal(clone)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}

func manifestDocument(t *testing.T) (map[string]any, json.RawMessage) {
	t.Helper()
	sum := sha256.Sum256([]byte("payload"))
	hexSum := hex.EncodeToString(sum[:])
	m := packagekit.Manifest{
		APIVersion: packagekit.Version,
		License:    "MIT OR Apache-2.0",
		Metadata:   map[string]json.RawMessage{"kvlite.module/v1": json.RawMessage(`{"entry":"main"}`)},
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/a", Revision: "r1"},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.a", Version: "v1"}, Operations: []plugin.Operation{{Name: "read"}}}}},
		Artifacts: []packagekit.Artifact{
			{Name: "source", Path: "source.txt", SHA256: hexSum},
			{Name: "vendor", Path: "vendor", Kind: packagekit.ArtifactTree, SHA256: hexSum},
		},
		Entrypoints: []packagekit.Entrypoint{{Name: "default", Runtime: "jsonline", Artifact: "source", Protocols: []string{plugin.APIVersion}}},
		Assets:      []packagekit.Asset{{Artifact: "vendor", Kind: "icons"}},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest fixture invalid: %v", err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, raw
}

// TestPairwiseMutationDifferential changes two independent positions of a valid
// descriptor, request, response and package manifest at once and requires the C
// engine and the Go code to agree on whether the result is valid.
func TestPairwiseMutationDifferential(t *testing.T) {
	data, err := os.ReadFile("../../lib/plugin/testdata/v1/descriptor.json")
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err = json.Unmarshal(data, &base); err != nil {
		t.Fatal(err)
	}
	var d plugin.Descriptor
	if err = plugin.Decode(data, &d); err != nil || d.Validate() != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	request := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "plugin": base["identity"], "contract": map[string]any{"name": "ext.conformance", "version": "v1"}, "operation": "echo", "payload": map[string]any{"value": 7}, "deadline": "2099-01-01T00:00:00Z"}
	response := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "payload": map[string]any{"value": 7}}
	failure := map[string]any{"apiVersion": "ext.plugin/v1", "id": "1", "error": map[string]any{"code": "busy", "message": "m", "retryAfterMilliseconds": 10}}
	manifest, _ := manifestDocument(t)

	// EXT_PAIR_SCALE multiplies every sample size, for a longer search.
	scale := 1
	if v, err := strconv.Atoi(os.Getenv("EXT_PAIR_SCALE")); err == nil && v > 0 {
		scale = v
	}
	type result struct{ total, accepted, diverged int }
	run := func(name string, doc any, count int, seed int64, check func(raw json.RawMessage) (want, got error)) {
		var r result
		for _, raw := range pairMutations(doc, count*scale, seed) {
			want, got := check(raw)
			r.total++
			if want == nil {
				r.accepted++
			}
			if (want == nil) != (got == nil) {
				r.diverged++
				if r.diverged <= 8 {
					t.Errorf("%s: %s Go=%s C=%s", name, raw, errText(want), errText(got))
				}
			}
		}
		t.Logf("%s: %d pair mutations (%d accepted), %d divergences", name, r.total, r.accepted, r.diverged)
		if r.accepted == 0 || r.accepted == r.total {
			t.Errorf("%s pair mutations are vacuous: accepted=%d of %d", name, r.accepted, r.total)
		}
	}
	run("descriptor", base, 4000, 1, func(raw json.RawMessage) (error, error) {
		var v plugin.Descriptor
		want := plugin.Decode(raw, &v)
		if want == nil {
			want = v.Validate()
		}
		_, got := EngineCall("descriptor.validate", raw)
		return want, got
	})
	run("request", request, 4000, 2, func(raw json.RawMessage) (error, error) {
		var r plugin.Request
		want := plugin.Decode(raw, &r)
		if want == nil {
			want = plugin.ValidateRequest(d, r)
		}
		_, got := service(t, "request.validate", map[string]any{"descriptor": json.RawMessage(data), "request": raw})
		return want, got
	})
	for i, doc := range []map[string]any{response, failure} {
		name := []string{"response", "failure"}[i]
		run(name, doc, 3000, int64(3+i), func(raw json.RawMessage) (error, error) {
			var r plugin.Response
			want := plugin.Decode(raw, &r)
			if want == nil {
				want = r.Validate("1")
			}
			_, got := service(t, "response.validate", map[string]any{"requestID": "1", "response": raw})
			return want, got
		})
	}
	run("manifest", manifest, 6000, 5, func(raw json.RawMessage) (error, error) {
		_, want := packagekit.Decode(raw)
		_, got := EngineCall("package.validate", raw)
		return want, got
	})
}
