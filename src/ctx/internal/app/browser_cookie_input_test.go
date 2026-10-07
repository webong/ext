package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func cookieCLIAdapter(t *testing.T, operations, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	t.Setenv("CTX_ADAPTER_HOME", filepath.Join(root, "state", "adapters"))
	t.Setenv("CTX_BROWSER", "")
	source := filepath.Join(root, "adapter")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := "api_version = \"2.0\"\nname = \"fixture\"\nruntime = \"browser\"\nsurfaces = \"web\"\nexecutable = \"adapter\"\ncapabilities = \"list,share,validate,doctor\"\nselector_key = \"browser\"\nshare_spaces = \"browser\"\nbrowser_share = \"" + operations + "\"\n"
	if err := os.WriteFile(filepath.Join(source, "adapter.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "adapter"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := adapterStore()
	a, err := store.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(a); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCookieImportQueryAndNetscapeSelection(t *testing.T) {
	root := cookieCLIAdapter(t, "cookie.import", `if [ "$1" = list ]; then printf '%s\n' fixture:default; elif [ "$5" = import ]; then cat > "$CTX_TEST_COOKIE_REQUEST"; else exit 2; fi`)
	captured := filepath.Join(root, "request.json")
	t.Setenv("CTX_TEST_COOKIE_REQUEST", captured)
	run := func(input, name string) (int, string) {
		t.Helper()
		file := filepath.Join(root, "input")
		if err := os.WriteFile(file, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		args := []string{"share:browser", "cookie", "import", "--from-file", file, "--to-profile", "fixture:default", "--site", "https://example.test", "--name", name}
		var out, diagnostics bytes.Buffer
		return Run(args, &out, &diagnostics), diagnostics.String()
	}
	query := `{"cookies":[{"name":"csrf","value":"other","domain":"example.test","path":"/"},{"name":"session","value":"fixture-secret","domain":"example.test","path":"/","expiry":2000000000,"source":"custom:work"}],"warnings":["unreadable third cookie"]}`
	if code, diagnostics := run(query, "session"); code != 0 || !strings.Contains(diagnostics, "input query reported 1 warnings") {
		t.Fatalf("query import: exit=%d diagnostics=%s", code, diagnostics)
	}
	data, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	var request browserShareRequest
	if json.Unmarshal(data, &request) != nil || request.Bundle == nil || request.Bundle.Cookie.Value != "fixture-secret" || request.Bundle.Source != "custom:work" {
		t.Fatal("query import did not preserve selected cookie")
	}
	jar := "#HttpOnly_example.test\tFALSE\t/\tTRUE\t2000000000\tsession\tjar-secret\n"
	if code, diagnostics := run(jar, "session"); code != 0 {
		t.Fatalf("jar import: %d %s", code, diagnostics)
	}
	data, _ = os.ReadFile(captured)
	if json.Unmarshal(data, &request) != nil || !request.Bundle.Cookie.HTTPOnly || request.Bundle.Cookie.Domain != "example.test" {
		t.Fatal("jar scope changed")
	}
	_ = os.Remove(captured)
	duplicate := `[{"name":"session","value":"one","domain":"example.test","path":"/"},{"name":"session","value":"two","domain":".example.test","path":"/"}]`
	if code, _ := run(duplicate, "session"); code != 2 {
		t.Fatal("ambiguous cookie was imported")
	}
	if _, err := os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("ambiguous input reached importer")
	}
}

func TestCookieQueryStrictDoesNotWritePartialOutput(t *testing.T) {
	root := cookieCLIAdapter(t, "cookie.query", `if [ "$1" = list ]; then printf '%s\n' fixture:default; else printf '%s' '{"cookies":[{"name":"session","value":"fixture-secret","domain":"example.test","path":"/"}],"warnings":["unavailable key"]}'; fi`)
	output := filepath.Join(root, "cookies.json")
	args := []string{"share:browser", "cookie", "query", "--from", "fixture:default", "--site", "https://example.test", "--strict", "--to-file", output}
	var out, diagnostics bytes.Buffer
	if code := Run(args, &out, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "incomplete") {
		t.Fatalf("strict: exit=%d diagnostics=%s", code, diagnostics.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("strict query wrote a partial result")
	}
	input := filepath.Join(root, "empty.json")
	if err := os.WriteFile(input, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	args = []string{"share:browser", "cookie", "query", "--inline-only", "--inline-file", input, "--site", "https://example.test", "--require-match", "--to-file", output}
	if code := Run(args, &out, &diagnostics); code != 1 {
		t.Fatal("required match accepted an empty result")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("empty required query created output")
	}
}
