package arch_test

import (
	"path/filepath"
	"testing"
)

// session.endpoint reports a browser-level debugging endpoint read from a marker
// the browser publishes in its profile. Only adapters whose engine implements
// guest.EndpointBackend may declare it, so the list below is explicit: adding an
// adapter here means its native backend answers the operation and has tests.
func TestSessionEndpointIsDeclaredOnlyWhereImplemented(t *testing.T) {
	implemented := map[string]bool{"chrome": true, "chromium": true, "edge": true}
	manifests, err := filepath.Glob(filepath.Join(repoRoot(t), "adapters", "*", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, path := range manifests {
		values := manifestValues(t, path)
		operations := map[string]bool{}
		for _, operation := range splitList(values["browser_management"]) {
			operations[operation] = true
		}
		if !operations["session.endpoint"] {
			continue
		}
		name := values["name"]
		declared[name] = true
		if !implemented[name] {
			t.Errorf("%s declares session.endpoint but no endpoint backend is registered for it", name)
		}
		if !operations["session.targets"] {
			t.Errorf("%s declares session.endpoint without session.targets", name)
		}
	}
	for name := range implemented {
		if !declared[name] {
			t.Errorf("%s implements session.endpoint but does not declare it", name)
		}
	}
}
