package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCookieQueryOutputFormats(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	input := filepath.Join(root, "input.json")
	if err := os.WriteFile(input, []byte(`[{"name":"session","value":"synthetic-value","domain":"example.test","path":"/","secure":true,"httpOnly":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "header", "netscape"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(root, format)
			args := []string{"share:browser", "cookie", "query", "--inline-only", "--inline-file", input,
				"--site", "https://example.test", "--format", format, "--to-file", path}
			var out, diagnostics bytes.Buffer
			if code := Run(args, &out, &diagnostics); code != 0 {
				t.Fatalf("format=%s code=%d err=%s", format, code, diagnostics.String())
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), "synthetic-value") || strings.Contains(out.String()+diagnostics.String(), "synthetic-value") {
				t.Fatal("value missing from protected output or exposed in diagnostics")
			}
			if format == "header" && string(data) != "Cookie: session=synthetic-value\n" {
				t.Fatal("unexpected header output")
			}
			if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatal("output file is not private")
			}
			if code := Run(args, &out, &diagnostics); code == 0 {
				t.Fatal("existing export overwritten")
			}
		})
	}
}

func TestCookieQueryRejectsInvalidOrLossyOutputBeforeWriting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	input := filepath.Join(root, "input.json")
	if err := os.WriteFile(input, []byte(`[{"name":"session","value":"synthetic-secret","domain":"example.test","path":"/","attributes":{"fixture.container":"work"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	for _, flags := range [][]string{
		{"--format", "unknown", "--site", "https://example.test"},
		{"--format", "header"},
		{"--format", "header", "--site", "https://example.test", "--site", "https://other.test"},
		{"--format", "header", "--site", "https://example.test", "--include-expired"},
		{"--format", "header", "--site", "https://example.test"},
		{"--format", "netscape", "--site", "https://example.test"},
	} {
		args := append([]string{"share:browser", "cookie", "query", "--inline-only", "--inline-file", input, "--to-file", output}, flags...)
		var out, diagnostics bytes.Buffer
		if code := Run(args, &out, &diagnostics); code == 0 || strings.Contains(diagnostics.String(), "synthetic-secret") {
			t.Fatal("invalid export accepted or secret leaked")
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("failed export created an output file")
		}
	}
}
