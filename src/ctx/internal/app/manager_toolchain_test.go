package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDockerConfigOverlayPrefersManagerPlugins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink setup requires Developer Mode")
	}
	root := t.TempDir()
	base := filepath.Join(root, "docker")
	global := filepath.Join(base, "cli-plugins")
	manager := filepath.Join(root, "manager")
	for _, dir := range []string{global, manager, filepath.Join(base, "contexts")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(global, "docker-buildx"), filepath.Join(global, "docker-compose"), filepath.Join(manager, "docker-buildx")} {
		if err := os.WriteFile(path, []byte("plugin"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "config.json"), []byte(`{"auths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", base)
	overlay, cleanup, err := dockerConfigOverlay(manager, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	buildx, err := os.Readlink(filepath.Join(overlay, "cli-plugins", "docker-buildx"))
	if err != nil || buildx != filepath.Join(manager, "docker-buildx") {
		t.Fatalf("buildx target %q: %v", buildx, err)
	}
	if _, err := os.Lstat(filepath.Join(overlay, "cli-plugins", "docker-compose")); !os.IsNotExist(err) {
		t.Fatalf("manager directory should not inherit global Compose: %v", err)
	}
	if _, err := os.Stat(filepath.Join(overlay, "contexts")); err != nil {
		t.Fatalf("contexts not preserved: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(overlay, "config.json"))
	if err != nil || string(config) != `{"auths":{}}` {
		t.Fatalf("config not preserved: %q, %v", config, err)
	}
}

func TestDockerPluginOverlayOnlyForPluginCommands(t *testing.T) {
	cases := []struct {
		operation string
		args      []string
		want      bool
	}{
		{"run", []string{"compose", "version"}, true},
		{"run", []string{"--context", "alpha", "buildx", "version"}, true},
		{"run", []string{"--config", "/tmp/docker", "buildx", "version"}, false},
		{"run", []string{"login", "registry.example"}, false},
		{"run", []string{"run", "alpine", "compose"}, false},
		{"build", nil, true},
		{"validate", []string{"compose"}, false},
	}
	for _, test := range cases {
		if got := dockerNeedsPluginOverlay(test.operation, test.args); got != test.want {
			t.Errorf("%s %v: got %t, want %t", test.operation, test.args, got, test.want)
		}
	}
}
