package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declarativeSource writes a data-only installer: a manifest and the file of
// patterns it names, with no executable.
func declarativeSource(t *testing.T, root, manifest string) string {
	t.Helper()
	directory := filepath.Join(root, "nvm")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "program_roots"), []byte("# nvm keeps one directory per version\n${NVM_DIR:-~/.nvm}/versions/node/*/bin\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteKeyValueManifest(directory, manifest); err != nil {
		t.Fatal(err)
	}
	return directory
}

const declarativeManifest = `api_version = "2.0"
name = "nvm"
runtime = "computer"
surfaces = "shell"
role = "installer"
selectable = "false"
capabilities = "programs"
program_roots = "program_roots"
package_files = "program_roots"
description = "nvm installer"
`

func TestDeclarativeInstallerHasNoExecutable(t *testing.T) {
	root := t.TempDir()
	source := declarativeSource(t, root, declarativeManifest)
	loaded, err := LoadDirectory(source)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Declarative() || len(loaded.Manifest.ProgramRoots) != 1 || loaded.Manifest.ProgramRoots[0] != "${NVM_DIR:-~/.nvm}/versions/node/*/bin" {
		t.Fatalf("roots = %#v", loaded.Manifest.ProgramRoots)
	}
	if !loaded.HasCapability("programs") || !contains(loaded.Manifest.Role, "installer") {
		t.Fatalf("manifest = %#v", loaded.Manifest)
	}
	if _, err := loaded.Command(Invocation{Operation: "programs"}); err == nil || !strings.Contains(err.Error(), "declarative") {
		t.Fatalf("a declarative adapter must never be run: %v", err)
	}

	// It installs, is trusted like any adapter, and a changed pattern file is not trusted.
	store := NewStore(filepath.Join(root, "installed"))
	installed, err := store.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Trust(installed); err != nil {
		t.Fatal(err)
	}
	if err := store.AssertTrusted(installed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed.Directory, "program_roots"), []byte("/etc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if trusted, err := store.IsTrusted(installed); err != nil || trusted {
		t.Fatalf("edited patterns remained trusted: trusted=%v err=%v", trusted, err)
	}
}

func TestDeclarativeInstallerIsRestricted(t *testing.T) {
	for name, change := range map[string]func(string) string{
		"not an installer": func(m string) string { return strings.Replace(m, `role = "installer"`, `role = "interpreter"`, 1) },
		"more capabilities": func(m string) string {
			return strings.Replace(m, `capabilities = "programs"`, `capabilities = "programs,run"`, 1)
		},
		"supports a kind": func(m string) string { return m + `supports = "node"` + "\n" },
		"selectable":      func(m string) string { return strings.Replace(m, `selectable = "false"`, `selectable = "true"`, 1) },
		"has commands":    func(m string) string { return m + `computer_commands = "node"` + "\n" },
		"names a path": func(m string) string {
			return strings.Replace(m, `program_roots = "program_roots"`, `program_roots = "../x"`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			source := declarativeSource(t, t.TempDir(), change(declarativeManifest))
			if _, err := LoadDirectory(source); err == nil {
				t.Fatal("expected the declarative adapter to be rejected")
			}
		})
	}
	// An empty pattern file is rejected too.
	source := declarativeSource(t, t.TempDir(), declarativeManifest)
	if err := os.WriteFile(filepath.Join(source, "program_roots"), []byte("# nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDirectory(source); err == nil {
		t.Fatal("an empty program_roots file must be rejected")
	}
}
