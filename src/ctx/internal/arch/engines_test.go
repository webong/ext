package arch_test

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// manifestValues reads the key = "value" lines of an adapter.toml.
func manifestValues(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
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
	manifests, err := filepath.Glob(filepath.Join(root, "adapters", "*", "adapter.toml"))
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
