//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/plugin-go/contract/validate/schema"
)

var (
	schemaTypes   = []string{"object", "array", "string", "number", "integer", "boolean", "null"}
	schemaNames   = []string{"a", "b", "é", "🙂", "x/y", "", "A", "a b"}
	schemaNumbers = []float64{0, 1, -1, 2, 0.5, 1.5, -0.5, 9007199254740992, -9007199254740992, 1e308, -1e308}
	schemaWords   = []string{"", "a", "ab", "abc", "é", "🙂", "🙂🙂", "é", "a\nb", "\u0000", "x/y"}
)

func pick[T any](r *rand.Rand, list []T) T { return list[r.Intn(len(list))] }

// genSchema builds a schema that combines several keywords, nests up to depth levels and, now
// and then, carries a keyword that does not belong to its type or a bound that contradicts another.
func genSchema(r *rand.Rand, depth int) schema.Schema {
	s := schema.Schema{Type: pick(r, schemaTypes)}
	if r.Intn(40) == 0 {
		s.Type = pick(r, []string{"unknown", "", "Object"})
	}
	maybe := func(percent int) bool { return r.Intn(100) < percent }
	num := func() *float64 { v := pick(r, schemaNumbers); return &v }
	count := func() int { return pick(r, []int{0, 0, 1, 2, 3, -1}) }
	if maybe(50) {
		switch s.Type {
		case "integer", "number":
			if maybe(60) {
				s.Minimum = num()
			}
			if maybe(60) {
				s.Maximum = num()
			}
		case "string":
			if maybe(50) {
				s.MinLength = count()
			}
			if maybe(50) {
				s.MaxLength = count()
			}
			if maybe(35) {
				for i := r.Intn(4); i >= 0; i-- {
					s.Enum = append(s.Enum, pick(r, schemaWords))
				}
			}
		case "array":
			if depth > 0 && maybe(70) {
				item := genSchema(r, depth-1)
				s.Items = &item
			}
			if maybe(50) {
				s.MinItems = count()
			}
			if maybe(50) {
				s.MaxItems = count()
			}
		case "object":
			s.AdditionalProperties = maybe(30)
			if depth > 0 {
				s.Properties = map[string]schema.Schema{}
				for i := r.Intn(4); i > 0; i-- {
					s.Properties[pick(r, schemaNames)] = genSchema(r, depth-1)
				}
			}
			for i := r.Intn(3); i > 0; i-- {
				name := pick(r, schemaNames)
				if len(s.Properties) > 0 && maybe(80) {
					for k := range s.Properties {
						name = k
						break
					}
				}
				s.Required = append(s.Required, name)
			}
		}
	}
	// A keyword the type does not allow.
	if maybe(4) {
		switch r.Intn(5) {
		case 0:
			s.MinLength = 1
		case 1:
			s.MaxItems = 2
		case 2:
			s.Minimum = num()
		case 3:
			s.Enum = []string{"a"}
		default:
			s.Required = []string{"a"}
		}
	}
	return s
}

func genNumber(r *rand.Rand, s schema.Schema) json.RawMessage {
	base := pick(r, schemaNumbers)
	if s.Minimum != nil && r.Intn(2) == 0 {
		base = *s.Minimum + float64(r.Intn(3)-1)
	} else if s.Maximum != nil && r.Intn(2) == 0 {
		base = *s.Maximum + float64(r.Intn(3)-1)
	}
	text := strconv.FormatFloat(base, 'g', -1, 64)
	switch r.Intn(8) {
	case 0:
		text = strconv.FormatFloat(base, 'f', 1, 64) // 2.0
	case 1:
		text = strconv.FormatFloat(base, 'e', -1, 64)
	case 2:
		text = "-0"
	}
	return json.RawMessage(text)
}

// genValue builds a value that tries to satisfy s, then, now and then, breaks it.
func genValue(r *rand.Rand, s schema.Schema, depth int) json.RawMessage {
	if r.Intn(8) == 0 {
		return json.RawMessage(pick(r, matrixValues()))
	}
	switch s.Type {
	case "string":
		if len(s.Enum) > 0 && r.Intn(5) != 0 {
			raw, _ := json.Marshal(pick(r, s.Enum))
			return raw
		}
		raw, _ := json.Marshal(strings.Repeat(pick(r, schemaWords), r.Intn(4)))
		return raw
	case "integer", "number":
		return genNumber(r, s)
	case "boolean":
		return json.RawMessage(pick(r, []string{"true", "false"}))
	case "array":
		n := r.Intn(4)
		if s.MinItems > 0 && r.Intn(3) != 0 {
			n = s.MinItems + r.Intn(2) - r.Intn(2)
		}
		if n < 0 {
			n = 0
		}
		var parts []string
		for i := 0; i < n; i++ {
			if s.Items != nil && depth > 0 {
				parts = append(parts, string(genValue(r, *s.Items, depth-1)))
			} else {
				parts = append(parts, pick(r, []string{"1", `"a"`, "null", "[]", "{}"}))
			}
		}
		return json.RawMessage("[" + strings.Join(parts, ",") + "]")
	case "object":
		var parts []string
		seen := map[string]bool{}
		for name, p := range s.Properties {
			if r.Intn(5) == 0 {
				continue
			}
			key, _ := json.Marshal(name)
			seen[name] = true
			value := json.RawMessage("null")
			if depth > 0 {
				value = genValue(r, p, depth-1)
			}
			parts = append(parts, string(key)+":"+string(value))
		}
		for _, name := range s.Required {
			if !seen[name] && r.Intn(3) != 0 {
				key, _ := json.Marshal(name)
				seen[name] = true
				parts = append(parts, string(key)+":1")
			}
		}
		if r.Intn(4) == 0 {
			key, _ := json.Marshal(pick(r, schemaNames))
			if !seen[string(key)] {
				parts = append(parts, string(key)+`:"extra"`)
			}
		}
		return json.RawMessage("{" + strings.Join(parts, ",") + "}")
	}
	return json.RawMessage("null")
}

// TestSchemaGeneratedDifferential compares the C engine and the Go schema package on seeded
// random schemas that combine several keywords and nest, and on values built to sit on and across
// the bounds they declare. EXT_PAIR_SCALE multiplies the sample.
func TestSchemaGeneratedDifferential(t *testing.T) {
	scale := 1
	if v, err := strconv.Atoi(os.Getenv("EXT_PAIR_SCALE")); err == nil && v > 0 {
		scale = v
	}
	r := rand.New(rand.NewSource(7))
	var schemas, valid, pairs, accepted, diverged int
	for i := 0; i < 3000*scale; i++ {
		s := genSchema(r, 3)
		raw, err := json.Marshal(s)
		if err != nil {
			continue
		}
		schemas++
		want := s.Validate()
		_, got := EngineCall("schema.validate", raw)
		if (want == nil) != (got == nil) {
			diverged++
			if diverged <= 8 {
				t.Errorf("schema %s: validate Go=%v C=%v", raw, errText(want), errText(got))
			}
			continue
		}
		if want != nil {
			continue
		}
		valid++
		for j := 0; j < 12; j++ {
			v := genValue(r, s, 3)
			w, g := s.Check(v), CheckSchema(s, v)
			pairs++
			if w == nil {
				accepted++
			}
			if (w == nil) != (g == nil) {
				diverged++
				if diverged <= 8 {
					t.Errorf("schema %s value %s: Go=%v C=%v", raw, v, errText(w), errText(g))
				}
			}
		}
	}
	t.Logf("%d schemas (%d valid), %d schema/value pairs (%d accepted), %d divergences", schemas, valid, pairs, accepted, diverged)
	if valid < schemas/4 || accepted < pairs/10 || accepted == pairs {
		t.Fatalf("the sample proves little: %d of %d schemas valid, %d of %d values accepted", valid, schemas, accepted, pairs)
	}
}
