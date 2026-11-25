package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCLIUsesGenerationAPI(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "stage")
	if err := os.WriteFile(filepath.Join(source, "binding.go"), []byte("//go:build example\n\npackage example\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := run([]string{"-translations", source, "-tags", "example", "-publish", destination}, &log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "binding_"+runtime.GOOS+"_"+runtime.GOARCH+".go")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte("staged 1 existing files")) {
		t.Fatalf("missing diagnostics: %s", &log)
	}
}
