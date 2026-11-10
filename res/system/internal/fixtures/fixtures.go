// Package fixtures finds and loads the language-neutral vectors under res/system/testdata, which
// the Swift, Java and C# bindings run as well. It is for tests.
package fixtures

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Dir returns the testdata directory of res/system: EXT_SYSTEM_TESTDATA when set, else the nearest
// one above the working directory. It skips the test when there is none, as when a module is
// tested on its own outside the repository.
func Dir(t testing.TB) string {
	t.Helper()
	if dir := os.Getenv("EXT_SYSTEM_TESTDATA"); dir != "" {
		return dir
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "testdata")
		if _, err := os.Stat(filepath.Join(candidate, "codes.json")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("res/system/testdata is not available")
		}
		dir = parent
	}
}

// Load decodes testdata/<area>/v<version>/cases.json into out and fails on an unknown field.
func Load(t testing.TB, area string, version int, out any) {
	t.Helper()
	path := filepath.Join(Dir(t), area, fmt.Sprintf("v%d", version), "cases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// Codes returns the error codes of testdata/codes.json.
func Codes(t testing.TB) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(Dir(t), "codes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, code := range file.Codes {
		codes[code] = true
	}
	return codes
}
