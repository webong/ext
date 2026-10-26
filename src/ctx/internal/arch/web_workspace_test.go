package arch_test

import (
	"path/filepath"
	"testing"
)

// The SDK ships the resource module's sources rather than a separately edited copy.
func TestWebKitSDKSourceOwnership(t *testing.T) {
	root := repoRoot(t)
	got, err := filepath.EvalSymlinks(filepath.Join(root, "pkg", "ext-ios", "Sources", "ExtWebKit"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "res", "web", "ios", "Sources", "ExtWebKit")
	if got != want {
		t.Fatalf("ExtWebKit source = %s, want %s", got, want)
	}
}

func TestClipboardSDKSourceOwnership(t *testing.T) {
	root := repoRoot(t)
	got, err := filepath.EvalSymlinks(filepath.Join(root, "pkg", "ext-ios", "Sources", "ExtClipboard"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "res", "system", "clipboard", "ios", "Sources", "ExtClipboard")
	if got != want {
		t.Fatalf("ExtClipboard source = %s, want %s", got, want)
	}
}

func TestSystemSDKSourceOwnership(t *testing.T) {
	root := repoRoot(t)
	got, err := filepath.EvalSymlinks(filepath.Join(root, "pkg", "ext-ios", "Sources", "ExtSystem"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "res", "system", "ios", "Sources", "ExtSystem")
	if got != want {
		t.Fatalf("ExtSystem source = %s, want %s", got, want)
	}
}

func TestNetworkSDKSourceOwnership(t *testing.T) {
	root := repoRoot(t)
	got, err := filepath.EvalSymlinks(filepath.Join(root, "pkg", "ext-ios", "Sources", "ExtNetwork"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "res", "system", "network", "ios", "Sources", "ExtNetwork")
	if got != want {
		t.Fatalf("ExtNetwork source = %s, want %s", got, want)
	}
}
