package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolverPrecedence(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "work", "project")
	child := filepath.Join(project, "child")
	configPath := filepath.Join(home, ".config", "ctx", "config.toml")
	writeFile(t, configPath, `docker_default = "fallback"

[profiles."client"]
docker = "profile-docker"
browser = "chrome:Default"

[profiles."client".env]
APP_ENV = "development"

[projects."`+project+`"]
profile = "client"
podman = "project-podman"
`)
	writeFile(t, filepath.Join(project, ".ctx"), `docker = "local-docker"`+"\n")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	resolver, err := NewResolver(child, home, configPath)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"docker":  "local-docker",
		"podman":  "project-podman",
		"browser": "chrome:Default",
		"profile": "client",
	}
	for key, expected := range tests {
		resolved, err := resolver.Resolve(key)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Value != expected {
			t.Fatalf("%s: got %q, want %q", key, resolved.Value, expected)
		}
	}
}

func TestLoadProfileEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, `[profiles."client"]
shell_path = "/client/bin"
[profiles."client".env]
REGION = "eu-west-1"
`)
	parsed, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Profiles["client"].Env["REGION"] != "eu-west-1" {
		t.Fatal("profile environment was not parsed")
	}
}
