package sourcecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveLocationPolicy(t *testing.T) {
	for _, tc := range []struct {
		source, archive, want string
		bad                   bool
	}{
		{"https://example.org/catalog/index.json", "tool.zip", "https://example.org/catalog/tool.zip", false},
		{"https://example.org/catalog/index.json", "https://other.org/tool.zip", "", true},
		{"https://example.org/index.json", "http://example.org/tool.zip", "", true},
		{"https://example.org/index.json", "https://user@example.org/tool.zip", "", true},
		{"https://example.org/index.json", "tool.zip?secret=value", "", true},
		{"https://example.org/index.json", "tool.zip#fragment", "", true},
		{filepath.Join(t.TempDir(), "index.json"), "../tool.zip", "", true},
		{filepath.Join(t.TempDir(), "index.json"), "%2e%2e/tool.zip", "", true},
		{filepath.Join(t.TempDir(), "index.json"), "https://example.org/tool.zip", "", true},
	} {
		got, err := archiveLocation(tc.source, tc.archive)
		if (err != nil) != tc.bad || !tc.bad && got != tc.want {
			t.Errorf("%s %s: %q %v", tc.source, tc.archive, got, err)
		}
	}
	for _, value := range []string{"http://example.org/index.json", "https://user:password@example.org/index.json", "https://example.org/index.json?token=x", "https://example.org/index.json#x"} {
		if _, err := location(value); err == nil {
			t.Errorf("accepted source %s", value)
		}
	}
}

func TestSnapshotRoundTripAndTamper(t *testing.T) {
	m := Manager{Home: t.TempDir()}
	// Canonicalize before hashing, as snapshot does. Persisting an indented document
	// must not invalidate an embedded snapshot (including HTML-sensitive bytes).
	index, err := json.Marshal(json.RawMessage(`{"format_version":1,"name":"example/tool","packages":{"linux/amd64":{"url":"x&y.zip","sha256":"` + strings.Repeat("a", 64) + `"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(index)
	d := document{Version: 1, Sources: map[string]Source{"tools": {Location: "/tmp/index.json", Index: index, SHA256: hex.EncodeToString(sum[:])}}}
	if err := m.save(d); err != nil {
		t.Fatal(err)
	}
	loaded, err := m.load()
	if err != nil || identity(loaded.Sources["tools"]) != "example/tool" {
		t.Fatalf("roundtrip: %+v %v", loaded, err)
	}
	data, err := os.ReadFile(filepath.Join(m.Home, "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "example/tool", "example/evil", 1))
	if err := os.WriteFile(filepath.Join(m.Home, "sources.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.load(); err == nil {
		t.Fatal("accepted changed snapshot")
	}
}
