package schema_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/webong/ctx/pkg/plugin/schema"
)

func TestSchema(t *testing.T) {
	s := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"items": {Type: "array", Items: &schema.Schema{Type: "integer"}, MaxItems: 2}}, Required: []string{"items"}}
	for _, v := range []string{`{"items":[1,2]}`, `{"items":[]}`} {
		if err := s.Check(json.RawMessage(v)); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{`{"items":[1.2]}`, `{"items":[1,2,3]}`, `{"items":null}`, `{}`, `{"items":[],"extra":1}`} {
		if s.Check(json.RawMessage(v)) == nil {
			t.Fatal(v)
		}
	}
	for _, s := range []schema.Schema{{Type: "array"}, {Type: "unknown"}, {Type: "string", Properties: map[string]schema.Schema{"a": {Type: "string"}}}, {Type: "object", Required: []string{"missing"}}} {
		if s.Validate() == nil {
			t.Fatal(s)
		}
	}
}

func TestSharedSchemaFixtures(t *testing.T) {
	data, err := os.ReadFile("../testdata/schema-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Schema  schema.Schema     `json:"schema"`
		Valid   []json.RawMessage `json:"valid"`
		Invalid []json.RawMessage `json:"invalid"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		for _, v := range c.Valid {
			if err := c.Schema.Check(v); err != nil {
				t.Fatalf("valid %s: %v", v, err)
			}
		}
		for _, v := range c.Invalid {
			if c.Schema.Check(v) == nil {
				t.Fatalf("accepted %s", v)
			}
		}
	}
}
