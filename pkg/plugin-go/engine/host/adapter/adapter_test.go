package adapter

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func fixtureAdapter(t *testing.T, root, name, runtimeName string) string {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	selector := name
	capabilities := "list,validate,run,doctor"
	surfaces := "shell"
	commands := "commands = \"" + name + "\"\n"
	if runtimeName == "browser" {
		selector = "browser"
		capabilities = "list,validate,open,doctor"
		surfaces = "web"
		commands = ""
	} else if runtimeName == "manager" {
		capabilities = "list,validate,run,doctor,image_save"
	}
	manifest := `api_version = "2.0"
name = "` + name + `"
runtime = "` + runtimeName + `"
surfaces = "` + surfaces + `"
executable = "ctx-` + name + `"
description = "Test adapter"
capabilities = "` + capabilities + `"
selector_key = "` + selector + `"
` + commands
	if err := writeText(filepath.Join(directory, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(filepath.Join(directory, "ctx-"+name), executable, 0o755); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestStoreInstallTrustAndTamper(t *testing.T) {
	root := t.TempDir()
	source := fixtureAdapter(t, filepath.Join(root, "source"), "echo", "computer")
	store := NewStore(filepath.Join(root, "installed"))
	installed, err := store.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if trusted, err := store.IsTrusted(installed); err != nil || trusted {
		t.Fatalf("new adapter unexpectedly trusted: trusted=%v err=%v", trusted, err)
	}
	if err := store.Trust(installed); err != nil {
		t.Fatal(err)
	}
	if err := store.AssertTrusted(installed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed.ExecutablePath(), []byte("changed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if trusted, err := store.IsTrusted(installed); err != nil || trusted {
		t.Fatalf("modified adapter remained trusted: trusted=%v err=%v", trusted, err)
	}
}

func TestNonselectableShareAdapter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "vault")
	if err := os.MkdirAll(path, 0o700); err != nil {
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
	if err := writeText(filepath.Join(path, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "ctx-vault"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDirectory(path)
	if err != nil || loaded.IsSelectable() || !loaded.HasCapability("share") {
		t.Fatalf("nonselectable share adapter: loaded=%v err=%v", loaded, err)
	}
}

func TestAdapterDisplayName(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "example", "browser")
	path := filepath.Join(directory, "plugin.json")
	original, err := readText(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDirectory(directory)
	if err != nil || loaded.DisplayLabel() != "example" {
		t.Fatalf("default display label=%v err=%v", loaded, err)
	}
	for _, test := range []struct {
		name  string
		valid bool
	}{
		{"Example App", true},
		{" Example App", false},
		{"Example\nApp", false},
		{"Example\u202eApp", false},
		{strings.Repeat("A", 81), false},
	} {
		manifest := string(original) + "display_name = " + strconv.Quote(test.name) + "\n"
		if err := writeText(path, []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadDirectory(directory)
		if test.valid {
			if err != nil || loaded.DisplayLabel() != test.name {
				t.Fatalf("display name %q: loaded=%v err=%v", test.name, loaded, err)
			}
		} else if err == nil {
			t.Fatalf("invalid display name %q accepted", test.name)
		}
	}
}

func TestPlatformDependencyDeclaration(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "chrome", "browser")
	path := filepath.Join(directory, "plugin.json")
	original, err := readText(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(original) + "dependencies_darwin = \"credential:keychain@2.0\"\n" +
		"dependencies_linux = \"credential:secret_service@2.0\"\n"
	if err := writeText(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ platform, name string }{{"darwin", "keychain"}, {"linux", "secret_service"}, {"windows", ""}} {
		loaded, err := LoadDirectoryForOS(directory, test.platform)
		if err != nil {
			t.Fatal(err)
		}
		if got := loaded.Manifest.Dependencies["credential"].Adapter; got != test.name {
			t.Fatalf("%s dependency=%q, want %q", test.platform, got, test.name)
		}
	}
	invalid := strings.Replace(manifest, "keychain@2.0", "keychain@1.0", 1)
	if err := writeText(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDirectoryForOS(directory, "linux"); err == nil {
		t.Fatal("invalid dependency on another platform was accepted")
	}
}

func TestValidateDependencyContract(t *testing.T) {
	requirement := Dependency{Space: "credential", Adapter: "vault", APIVersion: "2.0"}
	target := &Adapter{Manifest: Manifest{Name: "vault", APIVersion: "2.0", Capabilities: []string{"share"}, ShareSpaces: []string{"credential"}}}
	if err := ValidateDependency(requirement, target); err != nil {
		t.Fatal(err)
	}
	target.Manifest.ShareSpaces = []string{"browser"}
	if err := ValidateDependency(requirement, target); err == nil {
		t.Fatal("wrong share space satisfied dependency")
	}
	target.Manifest.ShareSpaces = []string{"credential"}
	target.Manifest.APIVersion = "1.0"
	if err := ValidateDependency(requirement, target); err == nil {
		t.Fatal("wrong API version satisfied dependency")
	}
}

func TestStoreReplace(t *testing.T) {
	root := t.TempDir()
	first := fixtureAdapter(t, filepath.Join(root, "first"), "echo", "computer")
	second := fixtureAdapter(t, filepath.Join(root, "second"), "echo", "computer")
	if err := os.WriteFile(filepath.Join(second, "extra"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(root, "installed"))
	if _, err := store.Install(first); err != nil {
		t.Fatal(err)
	}
	replaced, err := store.Replace(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(replaced.Directory, "extra")); err != nil {
		t.Fatalf("replacement did not install updated contents: %v", err)
	}
}

func TestManifestRuntimeAndSurface(t *testing.T) {
	root := t.TempDir()
	directory := fixtureAdapter(t, root, "firefox", "browser")
	loaded, err := LoadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Runtime != "browser" || !loaded.SupportsSurface("web") || loaded.Manifest.SelectorKey != "browser" || !loaded.HasCapability("open") {
		t.Fatalf("unexpected manifest: %#v", loaded.Manifest)
	}
}

func TestBrowserManagementManifestOperations(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "browser_mgr", "browser")
	path := filepath.Join(directory, "plugin.json")
	contents, err := readText(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(contents), `capabilities = "list,validate,open,doctor"`, `capabilities = "list,validate,open,doctor,share"`, 1) + `browser_management = "extension.prepare,extension.install,extension.activate,userscript.install,bookmarklet.encode"` + "\n"
	if err := writeText(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.HasBrowserManagement("extension.prepare") || loaded.HasBrowserManagement("extension.sign") {
		t.Fatalf("unexpected management operations: %v", loaded.Manifest.BrowserManagement)
	}
	// session.endpoint is a known operation.
	withEndpoint := strings.Replace(updated, "extension.prepare", "session.targets,session.endpoint", 1)
	if err := writeText(path, []byte(withEndpoint), 0o644); err != nil {
		t.Fatal(err)
	}
	endpointAdapter, err := LoadDirectory(directory)
	if err != nil || !endpointAdapter.HasBrowserManagement("session.endpoint") {
		t.Fatalf("session.endpoint was not accepted: %v", err)
	}
	for _, operation := range []string{"../prepare", "extension.unknown", "session.endpoints"} {
		invalid := strings.Replace(updated, "extension.prepare", operation, 1)
		if err := writeText(path, []byte(invalid), 0o644); err != nil {
			continue // the package contract refuses the operation name before the loader sees it
		}
		if _, err := LoadDirectory(directory); err == nil {
			t.Fatalf("invalid browser management operation %q was accepted", operation)
		}
	}
}

func TestSelfContainedAdapterOptInPreservesDefaultCommands(t *testing.T) {
	root := t.TempDir()
	directory := fixtureAdapter(t, root, "desktop", "manager")
	path := filepath.Join(directory, "plugin.json")
	data, err := readText(path)
	if err != nil {
		t.Fatal(err)
	}
	withoutCommands := strings.Replace(string(data), "commands = \"desktop\"\n", "", 1)
	if err := writeText(path, []byte(withoutCommands), 0o644); err != nil {
		t.Fatal(err)
	}
	defaulted, err := LoadDirectory(directory)
	if err != nil || !defaulted.HasCommand("desktop") {
		t.Fatalf("implicit command was lost: %v, %v", defaulted, err)
	}
	if err := writeText(path, []byte(withoutCommands+"self_contained = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selfContained, err := LoadDirectory(directory)
	if err != nil || !selfContained.Manifest.SelfContained || len(selfContained.Manifest.Commands) != 0 {
		t.Fatalf("self-contained adapter did not opt out: %v, %v", selfContained, err)
	}
}

func TestManifestRejectsUnknownKeys(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "echo", "computer")
	path := filepath.Join(directory, "plugin.json")
	original, err := readText(path)
	if err != nil {
		t.Fatal(err)
	}
	// "kind" was removed with the legacy manifest, and a typo must not be ignored
	for _, extra := range []string{"kind = \"selector\"\n", "colour = \"red\"\n"} {
		if err := writeText(path, append(append([]byte(nil), original...), extra...), 0o644); err == nil {
			if _, err := LoadDirectory(directory); err == nil {
				t.Fatalf("manifest with %q was accepted", strings.TrimSpace(extra))
			}
		}
	}
}

func TestManagerProviderCommandProtocol(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "engine", "manager")
	loaded, err := LoadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	command, err := loaded.Command(Invocation{
		Operation: "image_save", Selection: "remote", Arguments: []string{"image.tar", "example:dev"},
		Project: "/project", RealCommand: "/usr/bin/engine",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(command.Args, " "); !strings.Contains(got, "image_save remote -- image.tar example:dev") {
		t.Fatalf("unexpected command arguments: %q", got)
	}
	if environment := strings.Join(command.Env, "\n"); !strings.Contains(environment, "CTX_ADAPTER_REAL_COMMAND=/usr/bin/engine") {
		t.Fatalf("missing real command in environment: %q", environment)
	}
}

func TestManifestSelectsPlatformExecutable(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "echo", "computer")
	manifestPath := filepath.Join(directory, "plugin.json")
	manifest, err := readText(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = append(manifest, []byte("executable_windows = \"ctx-echo.ps1\"\n")...)
	if err := writeText(manifestPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "ctx-echo.ps1"), []byte("exit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(loaded.ExecutablePathForOS("windows")); got != "ctx-echo.ps1" {
		t.Fatalf("windows executable = %q", got)
	}
	if got := filepath.Base(loaded.ExecutablePathForOS("linux")); got != "ctx-echo" {
		t.Fatalf("linux executable = %q", got)
	}
}

func TestCommandProtocol(t *testing.T) {
	directory := fixtureAdapter(t, t.TempDir(), "echo", "computer")
	loaded, err := LoadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	command, err := loaded.Command(Invocation{
		Operation: "run", Selection: "staging", Arguments: []string{"hello"},
		Values: map[string]string{"echo": "staging"}, Profile: "client", Project: "/project", Command: "echoctl",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.Args, " ")
	if !strings.Contains(joined, "run staging -- hello") {
		t.Fatalf("unexpected command arguments: %q", joined)
	}
	environment := strings.Join(command.Env, "\n")
	for _, expected := range []string{"CTX_ADAPTER_API=2.0", "CTX_ADAPTER_NAME=echo", "CTX_ADAPTER_COMMAND=echoctl", "CTX_ADAPTER_VALUE_ECHO=staging"} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
}

func TestRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated Windows privileges")
	}
	directory := fixtureAdapter(t, t.TempDir(), "echo", "computer")
	if err := os.Symlink("plugin.json", filepath.Join(directory, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDirectory(directory); err == nil {
		t.Fatal("adapter containing a symlink was accepted")
	}
}

func TestUnknownRuntimeIsAcceptedButInert(t *testing.T) {
	root := t.TempDir()
	directory := fixtureAdapter(t, root, "notes", "content")
	manifestPath := filepath.Join(directory, "plugin.json")
	original, err := readText(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// A selectable adapter of a runtime this host does not define is refused.
	if _, err := LoadDirectory(directory); err == nil || !strings.Contains(err.Error(), "selectable") {
		t.Fatalf("selectable unknown runtime: %v", err)
	}
	inert := strings.ReplaceAll(string(original), "selector_key = \"notes\"\n", "selectable = \"false\"\n")
	inert = strings.ReplaceAll(inert, "commands = \"notes\"\n", "")
	if err := writeText(manifestPath, []byte(inert), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDirectory(directory)
	if err != nil {
		t.Fatalf("inert unknown runtime: %v", err)
	}
	if loaded.IsKnownRuntime() || loaded.IsSelectable() || !loaded.IsRuntime("content") {
		t.Fatalf("unexpected adapter: %#v", loaded.Manifest)
	}
	for _, name := range []string{"Bad Runtime", "share", "9lives", ""} {
		bad := strings.ReplaceAll(inert, `runtime = "content"`, `runtime = "`+name+`"`)
		if err := writeText(manifestPath, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDirectory(directory); err == nil {
			t.Fatalf("runtime %q must be rejected", name)
		}
	}
}

func TestSelfContainedComputerAdapterMayNameItsCommands(t *testing.T) {
	root := t.TempDir()
	directory := fixtureAdapter(t, root, "engine_tool", "computer")
	manifestPath := filepath.Join(directory, "plugin.json")
	original, err := readText(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// The adapter is its own implementation: it answers to a command but has no
	// native executable for the host to locate.
	contents := strings.ReplaceAll(string(original), "commands = \"engine_tool\"\n", "computer_commands = \"engine_tool\"\nself_contained = \"true\"\n")
	if err := writeText(manifestPath, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDirectory(directory)
	if err != nil {
		t.Fatalf("self-contained computer adapter: %v", err)
	}
	if !loaded.Manifest.SelfContained || !loaded.HasComputerCommand("engine_tool") {
		t.Fatalf("unexpected manifest: %#v", loaded.Manifest)
	}
	// Native commands still contradict self_contained.
	native := strings.ReplaceAll(contents, "self_contained", "commands = \"engine_tool\"\nself_contained")
	if err := writeText(manifestPath, []byte(native), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDirectory(directory); err == nil {
		t.Fatal("native commands with self_contained must be rejected")
	}
}

// readText renders the plugin.json at path in the compact key = "value" notation,
// so a test can change a field as text.
func readText(path string) ([]byte, error) {
	pm, err := readPackage(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	values, err := valuesFromPackage(pm)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var text strings.Builder
	for _, key := range keys {
		text.WriteString(key + " = " + strconv.Quote(values[key]) + "\n")
	}
	return []byte(text.String()), nil
}

// writeText replaces the plugin.json beside path with the template of text.
func writeText(path string, text []byte, _ os.FileMode) error {
	return WriteKeyValueManifest(filepath.Dir(path), string(text))
}
