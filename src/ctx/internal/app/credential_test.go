package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCredentialCLIUsesTrustedAdapterAndPrivateOutputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses a Unix host")
	}
	root := t.TempDir()
	t.Setenv("CTX_HOME", filepath.Join(root, "state"))
	t.Setenv("CTX_ADAPTER_HOME", filepath.Join(root, "state", "adapters"))
	t.Setenv("CTX_ADAPTER_NAME", "")
	t.Setenv("CTX_TEST_VAULT_DIR", filepath.Join(root, "vault"))
	if err := os.MkdirAll(filepath.Join(root, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version = "2.0"
name = "vault"
runtime = "computer"
surfaces = "shell"
executable = "ctx-vault"
capabilities = "validate,doctor,share"
share_spaces = "credential"
selectable = "false"
self_contained = "true"
`
	command := `#!/bin/sh
set -eu
[ "$1" = share ] && [ "$3" = -- ] && [ "$4" = credential ] || exit 2
target="$CTX_TEST_VAULT_DIR/$6"
case "$5" in
  get) [ "$#" -eq 6 ] || exit 2; cat "$target" ;;
  put)
    case "$#" in 6) ;; 7) [ "$7" = --replace ] || exit 2 ;; *) exit 2 ;; esac
    [ ! -e "$target" ] || [ "${7:-}" = --replace ] || exit 1
    cat > "$target" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(source, "adapter.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "ctx-vault"), []byte(command), 0o700); err != nil {
		t.Fatal(err)
	}
	store := adapterStore()
	installed, err := store.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(installed); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "input")
	if err := os.WriteFile(input, []byte("private-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run(append([]string{"credential"}, args...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	if code, _, errText := run("put", "vault:alpha", "--from-file", input); code != 0 {
		t.Fatalf("put code=%d diagnostics=%q", code, errText)
	}
	if code, _, errText := run("copy", "vault:alpha", "vault:beta"); code != 0 {
		t.Fatalf("copy code=%d diagnostics=%q", code, errText)
	}
	if code, _, _ := run("copy", "vault:alpha", "vault:beta"); code == 0 {
		t.Fatal("copy overwrote an existing item without --replace")
	}
	output := filepath.Join(root, "output")
	if code, _, errText := run("get", "vault:beta", "--to-file", output); code != 0 {
		t.Fatalf("get code=%d diagnostics=%q", code, errText)
	}
	value, err := os.ReadFile(output)
	if err != nil || string(value) != "private-value" {
		t.Fatalf("output=%q err=%v", value, err)
	}
	if info, err := os.Stat(output); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode=%v", info.Mode())
	}
	if code, out, errText := run("get", "vault:alpha", "--stdout"); code != 0 || out != "private-value" || errText != "" {
		t.Fatalf("stdout get code=%d output=%q diagnostics=%q", code, out, errText)
	}
	requesterSource := filepath.Join(root, "requester")
	if err := os.MkdirAll(requesterSource, 0o700); err != nil {
		t.Fatal(err)
	}
	requesterManifest := `api_version = "2.0"
name = "caller"
display_name = "Example App"
runtime = "computer"
surfaces = "shell"
executable = "ctx-caller"
capabilities = "validate,doctor,share"
selectable = "false"
self_contained = "true"
dependencies_darwin = "credential:vault@2.0"
dependencies_linux = "credential:vault@2.0"
`
	if err := os.WriteFile(filepath.Join(requesterSource, "adapter.toml"), []byte(requesterManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requesterSource, "ctx-caller"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	requester, err := store.Install(requesterSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(requester); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CTX_ADAPTER_NAME", "caller")
	if code, out, errText := run("get", "vault:alpha", "--stdout"); code != 0 || out != "private-value" ||
		!strings.Contains(errText, "reported requester Example App (caller) is accessing credentials through vault") {
		t.Fatalf("requester notice code=%d output=%q diagnostics=%q", code, out, errText)
	}
	t.Setenv("CTX_ADAPTER_NAME", "missing")
	if code, _, errText := run("get", "vault:alpha", "--stdout"); code != 0 || errText != "" {
		t.Fatalf("undeclared requester code=%d diagnostics=%q", code, errText)
	}
	t.Setenv("CTX_ADAPTER_NAME", "")
	if code, _, errText := run("get", "vault:alpha", "--to-file", output); code == 0 || !strings.Contains(errText, "already exists") {
		t.Fatalf("existing output code=%d diagnostics=%q", code, errText)
	}
	graph, err := os.ReadFile(filepath.Join(root, "state", "graph.json"))
	if err != nil || bytes.Contains(graph, []byte("private-value")) {
		t.Fatalf("credential leaked to graph or graph unavailable: %v", err)
	}
}
