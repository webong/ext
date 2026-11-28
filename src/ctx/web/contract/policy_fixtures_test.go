package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/webong/ext/src/ctx/web/internal/fixtures"
)

func TestPolicyWireFixtures(t *testing.T) {
	data, err := os.ReadFile(fixtures.Path(t, "policy", "v2", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Requests []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
			Valid bool            `json:"valid"`
		} `json:"requests"`
		Bundles []struct {
			Name  string       `json:"name"`
			Value PolicyBundle `json:"value"`
			Valid bool         `json:"valid"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Requests) == 0 || len(cases.Bundles) == 0 {
		t.Fatal("policy fixtures need request and bundle cases")
	}
	for _, c := range cases.Requests {
		t.Run("request/"+c.Name, func(t *testing.T) {
			_, err := DecodePolicyRequest(bytes.NewReader(c.Value))
			if valid := err == nil; valid != c.Valid {
				t.Fatalf("valid = %v, want %v", valid, c.Valid)
			}
		})
	}
	for _, c := range cases.Bundles {
		t.Run("bundle/"+c.Name, func(t *testing.T) {
			if valid := c.Value.Validate() == nil; valid != c.Valid {
				t.Fatalf("valid = %v, want %v", valid, c.Valid)
			}
		})
	}
}
