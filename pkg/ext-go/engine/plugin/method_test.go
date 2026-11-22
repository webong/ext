package plugin

import (
	"errors"
	"testing"
)

func TestResolveMethod(t *testing.T) {
	op := []Operation{{Name: "x"}}
	d := Descriptor{APIVersion: APIVersion, Identity: Identity{ID: "example/p", Revision: "r1"}, Contracts: []Contract{
		{ContractRef: ContractRef{Name: "example.content", Version: "v1"}, Operations: op},
		{ContractRef: ContractRef{Name: "example.content.model", Version: "v1"}, Operations: op},
		{ContractRef: ContractRef{Name: "other.provider", Version: "v2"}, Operations: op},
	}}
	cases := []struct {
		method, contract, operation string
		err                         bool
	}{
		{"example.content.observe", "example.content", "observe", false},
		{"example.content.model.inspect", "example.content.model", "inspect", false}, // longest prefix wins
		{"example.content.models.list", "example.content", "models.list", false},
		{"other.observe", "other.provider", "observe", false}, // unique first-segment fallback
		{"other.provider.observe", "other.provider", "observe", false},
		{"example.observe", "", "", true}, // two contracts start with "example."
		{"missing.observe", "", "", true},
		{"other.", "", "", true},
		{"other", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		ref, operation, err := d.ResolveMethod(c.method)
		if c.err {
			if !errors.Is(err, ErrUnsupported) {
				t.Errorf("%q: want unsupported, got %v", c.method, err)
			}
			continue
		}
		if err != nil || ref.Name != c.contract || operation != c.operation {
			t.Errorf("%q: got %v %q %v, want %q %q", c.method, ref, operation, err, c.contract, c.operation)
		}
		if Method(ref, operation) != c.method && c.method != "other.observe" {
			t.Errorf("%q: Method does not invert the resolution", c.method)
		}
	}
}
