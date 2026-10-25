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
