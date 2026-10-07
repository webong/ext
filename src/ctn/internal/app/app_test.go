package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"no arguments print usage", nil, 0, "Usage:", ""},
		{"version", []string{"version"}, 0, "ctn " + Version, ""},
		{"unknown command", []string{"bogus"}, 2, "", `unknown command "bogus"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(test.args, &stdout, &stderr); code != test.code {
				t.Fatalf("exit code = %d, want %d", code, test.code)
			}
			if !strings.Contains(stdout.String(), test.stdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), test.stdout)
			}
			if !strings.Contains(stderr.String(), test.stderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), test.stderr)
			}
		})
	}
}

func TestAdapterListSharesTheInstalledStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("EXT_HOME", home)
	t.Setenv("EXT_ADAPTER_HOME", "")
	t.Setenv("CTX_HOME", "")
	t.Setenv("CTX_ADAPTER_HOME", "")
	directory := filepath.Join(home, "adapters", "notes")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "api_version = \"2.0\"\nname = \"notes\"\nruntime = \"content\"\nsurfaces = \"shell\"\n" +
		"executable = \"ext-notes\"\ndescription = \"Notes\"\ncapabilities = \"list,validate,run,doctor\"\nselectable = \"false\"\n"
	if err := os.WriteFile(filepath.Join(directory, "adapter.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "ext-notes"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"adapter", "ls"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	line := strings.TrimSpace(stdout.String())
	if line != "notes\tcontent\tuntrusted (runtime unknown to this host)" {
		t.Fatalf("unexpected listing %q", line)
	}
	if code := Run([]string{"adapter", "bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("bogus subcommand exit %d", code)
	}
}
