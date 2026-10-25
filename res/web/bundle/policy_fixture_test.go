package bundle

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedMobilePolicyFixtures(t *testing.T) {
	data, err := os.ReadFile("../testdata/bundle/policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Cases []struct {
			Name         string   `json:"name"`
			AllowNet     bool     `json:"allowNet"`
			AllowOrigins []string `json:"allowOrigins"`
			CSP          string   `json:"csp"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Cases) == 0 {
		t.Fatal("no policy fixtures")
	}
	for _, v := range vectors.Cases {
		t.Run(v.Name, func(t *testing.T) {
			if got := (Policy{AllowNet: v.AllowNet, AllowOrigins: v.AllowOrigins}).ContentSecurityPolicy(); got != v.CSP {
				t.Fatalf("CSP = %q, want %q", got, v.CSP)
			}
		})
	}
}
