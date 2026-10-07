package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectAndResolve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.json")
	manifest := `{"apiVersion":"ext.package/v1","descriptor":{"apiVersion":"ext.plugin/v1","identity":{"id":"example/a","revision":"r1"},"contracts":[{"name":"example.echo","version":"v1","operations":[{"name":"echo"}]}]},"artifacts":[{"name":"main","path":"main.js","sha256":"` + strings.Repeat("a", 64) + `"}],"entrypoints":[{"name":"main","runtime":"jsonline","artifact":"main","protocols":["ext.plugin/v1"]}]}`
	if err := os.WriteFile(path, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"inspect", "--entry", "main", path}, &out); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["trusted"] != false || report["artifactsVerified"] != false || report["valid"] != true {
		t.Fatal(report)
	}
	out.Reset()
	if err := run([]string{"resolve", path}, &out); err != nil {
		t.Fatal(err)
	}
}
