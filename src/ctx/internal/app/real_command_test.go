package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocateRealCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses executable bits")
	}
	bin := t.TempDir()
	python3 := filepath.Join(bin, "python3")
	if err := os.WriteFile(python3, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	// The typed name wins when it exists.
	if got, err := locateRealCommand("python3", []string{"python", "python3"}, false); err != nil || got != python3 {
		t.Fatalf("typed name = %q, %v", got, err)
	}
	// A missing typed name falls back to another command the adapter declares.
	if got, err := locateRealCommand("python", []string{"python", "python3"}, false); err != nil || got != python3 {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	// Nothing declared is on PATH: an error, unless the adapter finds its own programs.
	if _, err := locateRealCommand("python", []string{"python", "python2"}, false); err == nil || !strings.Contains(err.Error(), "python CLI not found on PATH") {
		t.Fatalf("no discovery: %v", err)
	}
	if got, err := locateRealCommand("python", []string{"python", "python2"}, true); err != nil || got != "" {
		t.Fatalf("with discovery = %q, %v; want the adapter left to decide", got, err)
	}
}
