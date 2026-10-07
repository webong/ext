//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/plugin"
)

// edgeValues are substituted into every position of a valid document.
func edgeValues() []any {
	return []any{
		nil, true, false, 0, -1, 1, 1.5, 1e308, "", " ", "a", "ctx.plugin/v1", "ctx.plugin/v2", "Ünï", "🙂", "a b", "a\nb", "a\x00b",
		strings.Repeat("a", 64), strings.Repeat("a", 65), strings.Repeat("a", 129), strings.Repeat("a", 1025), "../x", "a/b", "UPPER", "-lead", "trail-",
		[]any{}, []any{"x"}, []any{map[string]any{}}, map[string]any{}, map[string]any{"name": "x"},
		"2099-01-01T00:00:00Z", "2000-01-01T00:00:00Z", "not-a-time", "2099-01-01T00:00:00+01:00", "2099-01-01 00:00:00",
	}
}

// mutations returns documents that differ from doc at exactly one position:
// the value replaced by each edge value, or the key removed.
func mutations(doc any) []json.RawMessage {
	var out []json.RawMessage
	var walk func(node any, path []any)
	emit := func(path []any, replace func(parent any) any) {
		clone := deepCopy(doc)
		holder := []any{clone}
		cur := any(holder)
		_ = cur
		var set func(node any, path []any) any
		set = func(node any, path []any) any {
			if len(path) == 0 {
				return replace(node)
			}
			switch n := node.(type) {
			case map[string]any:
				key := path[0].(string)
				if len(path) == 1 {
					res := replace(n[key])
					if res == removeMarker {
						delete(n, key)
					} else {
						n[key] = res
					}
				} else {
					n[key] = set(n[key], path[1:])
				}
			case []any:
				i := path[0].(int)
				if len(path) == 1 {
					res := replace(n[i])
					if res == removeMarker {
						n = append(n[:i], n[i+1:]...)
					} else {
						n[i] = res
					}
					return n
				}
				n[i] = set(n[i], path[1:])
			}
			return node
		}
		clone = set(clone, path)
		if raw, err := json.Marshal(clone); err == nil {
			out = append(out, raw)
		}
	}
	walk = func(node any, path []any) {
		switch n := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				p := append(append([]any{}, path...), k)
				emit(p, func(any) any { return removeMarker })
				for _, v := range edgeValues() {
					v := v
					emit(p, func(any) any { return v })
				}
				walk(n[k], p)
			}
			emit(append(append([]any{}, path...), "unexpected"), func(any) any { return "extra" })
		case []any:
			for i := range n {
				p := append(append([]any{}, path...), i)
				for _, v := range edgeValues() {
					v := v
					emit(p, func(any) any { return v })
				}
				walk(n[i], p)
			}
		}
	}
	walk(doc, nil)
	return out
}

type marker struct{}

var removeMarker any = &marker{}

func deepCopy(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func TestContractMutationDifferential(t *testing.T) {
	data, err := os.ReadFile("../../pkg/plugin/testdata/v1/descriptor.json")
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
	request := map[string]any{"apiVersion": "ctx.plugin/v1", "id": "1", "plugin": base["identity"], "contract": map[string]any{"name": "ctx.conformance", "version": "v1"}, "operation": "echo", "payload": map[string]any{"value": 7}, "deadline": "2099-01-01T00:00:00Z"}
	response := map[string]any{"apiVersion": "ctx.plugin/v1", "id": "1", "payload": map[string]any{"value": 7}}
	failure := map[string]any{"apiVersion": "ctx.plugin/v1", "id": "1", "error": map[string]any{"code": "busy", "message": "m", "retryAfterMilliseconds": 10}}

	goDescriptor := func(raw json.RawMessage) error {
		var v plugin.Descriptor
		if err := plugin.Decode(raw, &v); err != nil {
			return err
		}
		return v.Validate()
	}
	counts := map[string]int{}
	diverged := map[string][]string{}
	compare := func(kind string, raw json.RawMessage, want, got error) {
		counts[kind]++
		if want == nil {
			counts[kind+"/accepted"]++
		}
		if (want == nil) != (got == nil) {
			if len(diverged[kind]) < 8 {
				diverged[kind] = append(diverged[kind], string(raw)+" Go="+errText(want)+" C="+errText(got))
			}
			counts[kind+"!"]++
		}
	}
	for _, raw := range mutations(base) {
		_, got := EngineCall("descriptor.validate", raw)
		compare("descriptor", raw, goDescriptor(raw), got)
	}
	descriptorRaw := json.RawMessage(data)
	for _, raw := range mutations(request) {
		_, got := service(t, "request.validate", map[string]any{"descriptor": descriptorRaw, "request": raw})
		var r plugin.Request
		want := plugin.Decode(raw, &r)
		if want == nil {
			want = plugin.ValidateRequest(d, r)
		}
		compare("request", raw, want, got)
	}
	for name, doc := range map[string]map[string]any{"response": response, "failure": failure} {
		for _, raw := range mutations(doc) {
			_, got := service(t, "response.validate", map[string]any{"requestID": "1", "response": raw})
			var r plugin.Response
			want := plugin.Decode(raw, &r)
			if want == nil {
				want = r.Validate("1")
			}
			compare(name, raw, want, got)
		}
	}
	for _, kind := range []string{"descriptor", "request", "response", "failure"} {
		n, ok := counts[kind], counts[kind+"/accepted"]
		t.Logf("%s: %d mutations (%d accepted, %d rejected), %d divergences", kind, n, ok, n-ok, counts[kind+"!"])
		if ok == 0 || ok == n {
			t.Errorf("%s mutations are vacuous: accepted=%d of %d", kind, ok, n)
		}
	}
	for kind, list := range diverged {
		for _, line := range list {
			t.Errorf("%s: %s", kind, line)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}
