package jsonschema

import (
	"errors"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/plugin"
)

func TestKeywords(t *testing.T) {
	cases := []struct {
		name, schema string
		valid, bad   []string
	}{
		{"type", `{"type":"string"}`, []string{`"a"`}, []string{`1`, `null`, `[]`}},
		{"type list", `{"type":["string","null"]}`, []string{`"a"`, `null`}, []string{`1`}},
		{"integer accepts 3.0", `{"type":"integer"}`, []string{`3`, `3.0`, `3e2`}, []string{`3.5`, `"3"`}},
		{"number", `{"type":"number"}`, []string{`1`, `1.5`, `-2e-3`}, []string{`"1"`, `true`}},
		{"properties required additional", `{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"],"additionalProperties":false}`,
			[]string{`{"a":1}`}, []string{`{}`, `{"a":"x"}`, `{"a":1,"b":2}`}},
		{"additionalProperties schema", `{"properties":{"a":{}},"additionalProperties":{"type":"string"}}`, []string{`{"a":1,"b":"x"}`}, []string{`{"b":2}`}},
		{"items and bounds", `{"type":"array","items":{"type":"integer"},"minItems":1,"maxItems":2}`, []string{`[1]`, `[1,2]`}, []string{`[]`, `[1,2,3]`, `["a"]`}},
		{"prefixItems", `{"prefixItems":[{"type":"string"},{"type":"integer"}],"items":{"type":"boolean"}}`, []string{`["a",1,true]`}, []string{`[1]`, `["a",1,1]`}},
		{"uniqueItems", `{"uniqueItems":true}`, []string{`[1,2]`, `[1,"1"]`}, []string{`[1,1]`, `[1,1.0]`, `[{"a":1},{"a":1}]`}},
		{"enum and const", `{"enum":[1,"a",null,{"k":[1]}]}`, []string{`1`, `"a"`, `null`, `{"k":[1]}`}, []string{`2`, `"b"`, `{"k":[2]}`}},
		{"const", `{"const":{"a":1}}`, []string{`{"a":1}`, `{"a":1.0}`}, []string{`{"a":2}`}},
		{"string bounds count characters", `{"type":"string","minLength":2,"maxLength":3}`, []string{`"ab"`, `"é😀"`}, []string{`"a"`, `"abcd"`}},
		{"pattern", `{"type":"string","pattern":"^[a-z]+$"}`, []string{`"abc"`}, []string{`"Abc"`}},
		{"numeric bounds", `{"minimum":1,"maximum":3,"exclusiveMinimum":1,"exclusiveMaximum":3}`, []string{`2`}, []string{`1`, `3`, `0`}},
		{"multipleOf is exact", `{"multipleOf":0.1}`, []string{`0.3`, `1.1`}, []string{`0.35`}},
		{"minProperties maxProperties", `{"minProperties":1,"maxProperties":2}`, []string{`{"a":1}`}, []string{`{}`, `{"a":1,"b":2,"c":3}`}},
		{"allOf", `{"allOf":[{"type":"integer"},{"minimum":2}]}`, []string{`2`}, []string{`1`, `"x"`}},
		{"anyOf", `{"anyOf":[{"type":"integer"},{"type":"string"}]}`, []string{`1`, `"a"`}, []string{`null`}},
		{"oneOf", `{"oneOf":[{"type":"integer"},{"minimum":2}]}`, []string{`1`, `"x"`}, []string{`2`, `1.5`}},
		{"not", `{"not":{"type":"null"}}`, []string{`1`}, []string{`null`}},
		{"boolean schemas", `{"properties":{"a":true,"b":false}}`, []string{`{"a":1}`}, []string{`{"b":1}`}},
		{"local refs", `{"$defs":{"n":{"type":"integer"}},"properties":{"a":{"$ref":"#/$defs/n"}}}`, []string{`{"a":1}`}, []string{`{"a":"x"}`}},
		{"recursive ref", `{"$defs":{"t":{"type":"object","properties":{"c":{"$ref":"#/$defs/t"}},"additionalProperties":false}},"$ref":"#/$defs/t"}`, []string{`{"c":{"c":{}}}`}, []string{`{"c":{"d":1}}`}},
		{"annotations are ignored", `{"title":"t","description":"d","format":"email","default":1,"examples":[1],"$comment":"c"}`, []string{`"not an email"`}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := Compile([]byte(c.schema))
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range c.valid {
				if err := s.Validate([]byte(v)); err != nil {
					t.Errorf("%s rejected: %v", v, err)
				}
			}
			for _, v := range c.bad {
				err := s.Validate([]byte(v))
				if err == nil {
					t.Errorf("%s accepted", v)
				} else if !errors.Is(err, ErrInvalid) || !errors.Is(err, plugin.ErrInvalid) {
					t.Errorf("%s: error does not wrap ErrInvalid: %v", v, err)
				}
			}
		})
	}
}

func TestCompileRejects(t *testing.T) {
	for name, schema := range map[string]string{
		"empty":                "",
		"not JSON":             `{`,
		"trailing value":       `{} {}`,
		"duplicate key":        `{"type":"string","type":"string"}`,
		"unsupported keyword":  `{"dependentRequired":{"a":["b"]}}`,
		"unknown keyword":      `{"frobnicate":1}`,
		"bad type":             `{"type":"text"}`,
		"empty type list":      `{"type":[]}`,
		"empty enum":           `{"enum":[]}`,
		"required not array":   `{"required":"a"}`,
		"duplicate required":   `{"required":["a","a"]}`,
		"negative minLength":   `{"minLength":-1}`,
		"fractional maxItems":  `{"maxItems":1.5}`,
		"multipleOf zero":      `{"multipleOf":0}`,
		"bad pattern":          `{"pattern":"("}`,
		"remote ref":           `{"$ref":"https://example.test/schema"}`,
		"unresolved ref":       `{"$ref":"#/$defs/missing"}`,
		"ref to a non-schema":  `{"$defs":{"n":1},"$ref":"#/$defs/n"}`,
		"schema is a number":   `1`,
		"empty allOf":          `{"allOf":[]}`,
		"over 1 MiB":           `{"description":"` + strings.Repeat("a", 1<<20) + `"}`,
		"deep nesting":         strings.Repeat(`{"not":`, 70) + `{}` + strings.Repeat(`}`, 70),
		"unpaired surrogate":   `{"description":"\ud800"}`,
		"huge exponent bounds": `{"minimum":1e999999}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Compile([]byte(schema)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestValidateBounds(t *testing.T) {
	s, err := Compile([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"empty":              "",
		"not JSON":           `{`,
		"duplicate key":      `{"a":1,"a":2}`,
		"trailing":           `{} {}`,
		"unpaired surrogate": `{"a":"\udc00"}`,
		"too deep":           strings.Repeat(`[`, 70) + strings.Repeat(`]`, 70),
		"huge exponent":      `{"a":1e999999}`,
	} {
		if err := s.Validate([]byte(value)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	var nilSchema *Schema
	if err := nilSchema.Validate([]byte(`{}`)); err == nil {
		t.Fatal("nil schema accepted a value")
	}
}

func TestEvaluationWorkIsBounded(t *testing.T) {
	// A wide anyOf over a large array exceeds the evaluation budget instead of
	// running unbounded.
	branches := strings.TrimSuffix(strings.Repeat(`{"type":"string"},`, 50), ",")
	s, err := Compile([]byte(`{"type":"array","items":{"anyOf":[` + branches + `]}}`))
	if err != nil {
		t.Fatal(err)
	}
	items := strings.TrimSuffix(strings.Repeat(`1,`, 5000), ",")
	if err := s.Validate([]byte(`[` + items + `]`)); err == nil {
		t.Fatal("accepted")
	}
	// Self-reference cannot loop forever.
	loop, err := Compile([]byte(`{"$defs":{"a":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := loop.Validate([]byte(`1`)); err == nil {
		t.Fatal("cyclic reference accepted")
	}
}

func TestErrorsDoNotCarryValues(t *testing.T) {
	s, _ := Compile([]byte(`{"properties":{"a":{"type":"integer"}}}`))
	err := s.Validate([]byte(`{"a":"s3cr3t-value"}`))
	if err == nil || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("error leaks the value: %v", err)
	}
}

func TestValidateHelper(t *testing.T) {
	if err := Validate([]byte(`{"type":"integer"}`), []byte(`1`)); err != nil {
		t.Fatal(err)
	}
	if err := Validate([]byte(`{"type":"integer"}`), []byte(`"x"`)); err == nil {
		t.Fatal("accepted")
	}
	if err := Validate([]byte(`{"type":"nope"}`), []byte(`1`)); err == nil {
		t.Fatal("bad schema accepted")
	}
}
