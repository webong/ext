//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/webong/ctx/pkg/plugin/schema"
)

func ptr(v float64) *float64 { return &v }

// matrixSchemas covers every type and constraint, including nesting, boundary
// limits and combinations the validator must reject.
func matrixSchemas() []schema.Schema {
	text := schema.Schema{Type: "string"}
	return []schema.Schema{
		{Type: "string"}, {Type: "string", MinLength: 1}, {Type: "string", MaxLength: 2}, {Type: "string", MinLength: 2, MaxLength: 2},
		{Type: "string", Enum: []string{"a", "é", "🙂", ""}}, {Type: "string", MinLength: 3, MaxLength: 2},
		{Type: "integer"}, {Type: "integer", Minimum: ptr(0)}, {Type: "integer", Maximum: ptr(9007199254740992)},
		{Type: "integer", Minimum: ptr(-1), Maximum: ptr(1)}, {Type: "integer", Minimum: ptr(2), Maximum: ptr(1)},
		{Type: "number"}, {Type: "number", Minimum: ptr(0.5), Maximum: ptr(1.5)}, {Type: "number", Minimum: ptr(-1e308)},
		{Type: "boolean"}, {Type: "null"},
		{Type: "array"}, {Type: "array", Items: &text}, {Type: "array", MinItems: 1}, {Type: "array", MaxItems: 1, Items: &schema.Schema{Type: "integer"}},
		{Type: "array", MinItems: 2, MaxItems: 1},
		{Type: "object"}, {Type: "object", AdditionalProperties: true},
		{Type: "object", Properties: map[string]schema.Schema{"a": {Type: "integer"}, "b": text}, Required: []string{"a"}},
		{Type: "object", Properties: map[string]schema.Schema{"a": {Type: "integer"}}, Required: []string{"missing"}},
		{Type: "object", Properties: map[string]schema.Schema{"é": text, "🙂": {Type: "null"}}, Required: []string{"é"}, AdditionalProperties: true},
		{Type: "object", Properties: map[string]schema.Schema{"n": {Type: "object", Properties: map[string]schema.Schema{"x": {Type: "array", Items: &schema.Schema{Type: "number"}}}}}},
		// Constraints invalid for the declared type.
		{Type: "string", MinItems: 1}, {Type: "integer", MinLength: 1}, {Type: "boolean", Minimum: ptr(0)}, {Type: "array", Enum: []string{"a"}},
		{Type: "object", Items: &text}, {Type: "null", Required: []string{"a"}}, {Type: "unknown"}, {Type: ""},
	}
}

func matrixValues() []string {
	deep := strings.Repeat("[", 40) + strings.Repeat("]", 40)
	return []string{
		"null", "true", "false", "0", "-0", "1", "-1", "2", "0.5", "1.5", "1.0", "1e0", "1E2", "1e-2", "1e999", "-1e999", "1e-999",
		"9007199254740991", "9007199254740992", "9007199254740993", "-9007199254740993", "18446744073709551616", "1.7976931348623157e308", "-1.7976931348623157e308",
		`""`, `"a"`, `"ab"`, `"abc"`, `"é"`, `"🙂"`, `"🙂🙂"`, `"é"`, `"🙂"`, `"\ud83d"`, `"\u0000"`, `"a\nb"`, `"\/"`,
		"[]", "[1]", "[1,2]", `["a"]`, `["a","b"]`, "[null]", "[[]]", "[1.5]", "[1e2]", deep,
		"{}", `{"a":1}`, `{"a":1.5}`, `{"a":"x"}`, `{"a":1,"b":"x"}`, `{"a":1,"b":null}`, `{"b":"x"}`, `{"a":1,"c":1}`, `{"a":1,"a":2}`,
		`{"é":"x"}`, `{"é":"x","🙂":null}`, `{"é":"x","other":[1]}`, `{"n":{"x":[1,2.5]}}`, `{"n":{"x":["no"]}}`, `{"n":{"y":1}}`, `{"n":null}`,
		" 1 ", "\n[ ]\n", "", " ", "nul", "tru", "01", "+1", "1.", ".5", "0x10", "NaN", "Infinity", "-", "1,2", "[1,]", "{,}", `{"a":}`, `{a:1}`, `'a'`, `"unterminated`,
		"\x00", "[1]garbage", "{} {}",
	}
}

// knownDivergences lists values on which the engines intentionally differ.
// Go's decoder silently replaces an unpaired surrogate escape with U+FFFD; the C
// parser rejects it, which avoids silent data corruption. The test requires the
// difference to persist, so resolving it (in either direction) must update this
// table and docs/plugin-engine-parity.md together.
var knownDivergences = map[string]string{
	`"\ud83d"`: "unpaired high surrogate escape: Go accepts as U+FFFD, C rejects",
}

func TestSchemaMatrixDifferential(t *testing.T) {
	schemas, values := matrixSchemas(), matrixValues()
	checked, diverged := 0, 0
	for i, s := range schemas {
		goInvalid := s.Validate()
		raw, _ := json.Marshal(s)
		_, cInvalid := EngineCall("schema.validate", raw)
		if (goInvalid == nil) != (cInvalid == nil) {
			t.Fatalf("schema[%d] %s validate: Go=%v C=%v", i, raw, goInvalid, cInvalid)
		}
		if goInvalid != nil {
			continue
		}
		for _, v := range values {
			want := s.Check(json.RawMessage(v))
			got := CheckSchema(s, json.RawMessage(v))
			checked++
			if reason, known := knownDivergences[v]; known {
				if got == nil {
					t.Errorf("schema[%d] value %q: C now accepts it (%s): update knownDivergences", i, v, reason)
				}
				if want == nil {
					diverged++
				}
				continue
			}
			if (want == nil) != (got == nil) {
				t.Errorf("schema[%d] %s value %q: Go=%v C=%v", i, raw, v, want, got)
			}
		}
	}
	t.Logf("compared %d schema/value pairs; %d known divergences", checked, diverged)
	if diverged == 0 {
		t.Error("known divergence no longer observed: update knownDivergences and the parity doc")
	}
	if checked < 1000 {
		t.Fatal(fmt.Sprintf("matrix too small: %d", checked))
	}
}
