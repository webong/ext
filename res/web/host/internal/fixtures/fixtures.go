// Package fixtures locates the language-neutral fixtures in res/web/testdata.
// The Go, Swift, Java, .NET and JavaScript modules of res/web share them, so they
// sit beside the host module rather than inside it. A copy of the host module
// without the repository around it, such as the module cache, has none.
package fixtures

import (
	"os"
	"path/filepath"
	"testing"
)

// RepoPath returns the path of elem under the repository root, for a test that
// needs another part of the checkout, such as the example extensions built in
// pkg/ext-web. It follows the same rule as Path: it fails the test when the
// shared fixtures are missing in a checkout and skips outside one.
func RepoPath(t testing.TB, elem ...string) string {
	t.Helper()
	root := filepath.Join(Path(t), "..", "..", "..") // res/web/testdata is three levels below the root
	return filepath.Join(append([]string{root}, elem...)...)
}

// Path returns the path of elem under res/web/testdata. In a checkout of the
// repository it fails the test when the fixtures are missing; in a copy of the
// module without the repository it skips the test.
func Path(t testing.TB, elem ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		// res/web holds the host module and the shared fixtures side by side.
		if _, err := os.Stat(filepath.Join(dir, "host", "go.mod")); err == nil {
			root := filepath.Join(dir, "testdata")
			if _, err := os.Stat(root); err != nil {
				t.Fatalf("shared fixtures are missing: %v", err)
			}
			return filepath.Join(append([]string{root}, elem...)...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("res/web/testdata is not part of the host module; run this test in a checkout of the repository")
		}
		dir = parent
	}
}
