package adapter

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func clearHomeEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"EXT_HOME", "EXT_ADAPTER_HOME", "CTX_HOME", "CTX_ADAPTER_HOME"} {
		t.Setenv(name, "")
	}
}

func TestHomePrecedence(t *testing.T) {
	clearHomeEnvironment(t)
	t.Setenv("CTX_HOME", "/legacy")
	if got := ConfigHome(); got != "/legacy" {
		t.Fatalf("legacy CTX_HOME: %q", got)
	}
	t.Setenv("EXT_HOME", "/shared")
	if got := ConfigHome(); got != "/shared" {
		t.Fatalf("EXT_HOME must win over CTX_HOME: %q", got)
	}
	if got, want := Home(), filepath.Join("/shared", "adapters"); got != want {
		t.Fatalf("Home under EXT_HOME: %q, want %q", got, want)
	}
	t.Setenv("CTX_ADAPTER_HOME", "/legacy-adapters")
	if got := Home(); got != "/legacy-adapters" {
		t.Fatalf("explicit legacy adapter home: %q", got)
	}
	t.Setenv("EXT_ADAPTER_HOME", "/ext-adapters")
	if got := Home(); got != "/ext-adapters" {
		t.Fatalf("EXT_ADAPTER_HOME must win: %q", got)
	}
}

func TestDefaultHomeKeepsAnEarlierInstallation(t *testing.T) {
	clearHomeEnvironment(t)
	if runtime.GOOS == "windows" {
		t.Skip("default directories differ on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	shared, legacy := filepath.Join(home, ".config", "ext"), filepath.Join(home, ".config", "ctx")
	if got := ConfigHome(); got != shared {
		t.Fatalf("fresh machine: %q, want %q", got, shared)
	}
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ConfigHome(); got != legacy {
		t.Fatalf("earlier installation must keep working: %q, want %q", got, legacy)
	}
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ConfigHome(); got != shared {
		t.Fatalf("once the shared directory exists it wins: %q, want %q", got, shared)
	}
}
