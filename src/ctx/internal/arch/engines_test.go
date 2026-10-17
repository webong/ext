package arch_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// manifestValues reads an adapter's plugin.json and returns the fields the
// policy tests look at: the name, the operations of the ext.adapter contracts and
// the ctx.adapter/v1 metadata, with lists joined by commas.
func manifestValues(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Descriptor struct {
			Identity  struct{ ID string } `json:"identity"`
			Contracts []struct {
				Name       string
				Operations []struct{ Name string }
			} `json:"contracts"`
		} `json:"descriptor"`
		Metadata map[string]map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	values := map[string]string{"name": document.Descriptor.Identity.ID}
	operations := map[string]string{"ext.adapter": "capabilities", "ext.adapter.browser.share": "browser_share", "ext.adapter.browser.management": "browser_management"}
	for _, contract := range document.Descriptor.Contracts {
		key, known := operations[contract.Name]
		if !known {
			continue
		}
		var names []string
		for _, operation := range contract.Operations {
			names = append(names, operation.Name)
		}
		values[key] = strings.Join(names, ",")
	}
	for key, value := range document.Metadata["ctx.adapter/v1"] {
		switch typed := value.(type) {
		case string:
			values[key] = typed
		case []any:
			var items []string
			for _, item := range typed {
				items = append(items, fmt.Sprint(item))
			}
			values[key] = strings.Join(items, ",")
		default:
			values[key] = fmt.Sprint(typed)
		}
	}
	return values
}

func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// TestEngineAdaptersFollowTheContract checks the parts of docs/engine-adapters.md
// that a manifest can show: the declaration and the required operations.
func TestEngineAdaptersFollowTheContract(t *testing.T) {
	root := repoRoot(t)
	manifests, err := filepath.Glob(filepath.Join(root, "adapters", "*", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var engines []string
	for _, path := range manifests {
		values := manifestValues(t, path)
		supports := splitList(values["supports"])
		isEngine := false
		for _, kind := range supports {
			isEngine = isEngine || kind == "engine"
		}
		if !isEngine {
			continue
		}
		name := values["name"]
		engines = append(engines, name)
		if values["runtime"] != "computer" {
			t.Errorf("%s: an engine uses the computer runtime, not %q", name, values["runtime"])
		}
		var kinds []string
		for _, kind := range supports {
			if kind != "engine" {
				kinds = append(kinds, kind)
			}
		}
		if len(kinds) != 1 {
			t.Errorf("%s: supports must be \"engine,<kind>\" with exactly one kind, got %q", name, values["supports"])
			continue
		}
		if values["selector_key"] != kinds[0] {
			t.Errorf("%s: selector_key must be the engine kind %q, got %q", name, kinds[0], values["selector_key"])
		}
		capabilities := map[string]bool{}
		for _, capability := range splitList(values["capabilities"]) {
			capabilities[capability] = true
		}
		for _, required := range []string{"list", "observe", "validate", "run", "doctor"} {
			if !capabilities[required] {
				t.Errorf("%s: an engine must implement %s (capabilities = %q)", name, required, values["capabilities"])
			}
		}
		if values["computer_commands"] == "" {
			t.Errorf("%s: an engine must name the command it answers to in computer_commands", name)
		}
	}
	sort.Strings(engines)
	// The maintained engines. Adding one means deciding it belongs here.
	if got := strings.Join(engines, ","); got != "evm,jvm,wasm" {
		t.Errorf("engine adapters are %q, want evm,jvm,wasm: update this list and the docs together", got)
	}
}
