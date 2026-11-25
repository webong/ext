package resourcecmd

import (
	"strings"
	"testing"
)

func TestDependencyGrants(t *testing.T) {
	valid := `{"provider":{"id":"example/provider","revision":"provider-v1","version":"1.0.0"},"contract":{"name":"example.value","version":"v1"},"operation":"read"}`
	grants, err := parseGrants([]string{"--allow-dependency", valid})
	if err != nil || len(grants) != 1 || grants[0].Provider.Revision != "provider-v1" {
		t.Fatalf("valid grant: %v %v", grants, err)
	}
	if grants, err := parseGrants(nil); err != nil || len(grants) != 0 {
		t.Fatalf("default: %v %v", grants, err)
	}
	for name, args := range map[string][]string{
		"missing-value":     {"--allow-dependency"},
		"unknown-option":    {"--allow", valid},
		"malformed":         {"--allow-dependency", "{"},
		"unknown-field":     {"--allow-dependency", strings.Replace(valid, `"operation":"read"`, `"operation":"read","extra":true`, 1)},
		"missing-revision":  {"--allow-dependency", strings.Replace(valid, `"revision":"provider-v1",`, "", 1)},
		"missing-contract":  {"--allow-dependency", strings.Replace(valid, `"name":"example.value"`, `"name":""`, 1)},
		"missing-operation": {"--allow-dependency", strings.Replace(valid, `"operation":"read"`, `"operation":""`, 1)},
		"duplicate":         {"--allow-dependency", valid, "--allow-dependency", valid},
		"oversize":          {"--allow-dependency", strings.Repeat(" ", 4097)},
		"too-many":          make([]string, 130),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGrants(args); err == nil {
				t.Fatal("accepted invalid grant")
			}
		})
	}
}
