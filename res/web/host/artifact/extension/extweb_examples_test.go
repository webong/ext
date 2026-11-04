package extension

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/webong/ext/res/web/host/internal/fixtures"
)

// The example extensions that pkg/ext-web builds (npm run build:examples in
// pkg/ext-web) must go through the same inspection and packaging as any other
// extension: a manifest shape this package cannot read or pack would otherwise
// be found only by a user. The theme example is built by the library's own
// createThemeManifest, so it also covers what that function emits.
//
// EXT_WEB_DIST names the directory holding the built examples and makes a missing
// build a failure, which is how the web-js workflow runs it. Without it the test
// looks for pkg/ext-web/dist in the checkout and skips when it has not been built.
func TestExtWebExamplesPackage(t *testing.T) {
	dist := os.Getenv("EXT_WEB_DIST")
	if dist == "" {
		dist = fixtures.RepoPath(t, "pkg", "ext-web", "dist")
		if _, err := os.Stat(dist); err != nil {
			t.Skip("pkg/ext-web/dist is not built; run npm run build:examples in pkg/ext-web or set EXT_WEB_DIST")
		}
	}
	for _, name := range []string{"host", "guest", "theme", "firefox-host", "firefox-guest"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(dist, name)
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				t.Fatalf("example %s is not built in %s: %v", name, dist, err)
			}
			source, err := Inspect(dir)
			if err != nil {
				t.Fatalf("Inspect(%s): %v", dir, err)
			}
			if source.Name == "" || source.Version == "" || source.ManifestVersion != 3 {
				t.Fatalf("Inspect(%s) = %+v, want a named, versioned manifest version 3", dir, source)
			}
			archive := filepath.Join(t.TempDir(), name+".zip")
			packed, err := Package(dir, archive)
			if err != nil {
				t.Fatalf("Package(%s): %v", dir, err)
			}
			back, err := Inspect(archive)
			if err != nil {
				t.Fatalf("Inspect(packaged %s): %v", name, err)
			}
			if back.Name != source.Name || back.Version != source.Version || back.ManifestVersion != source.ManifestVersion ||
				back.ID != source.ID || !slices.Equal(back.Permissions, source.Permissions) || packed.Files != source.Files {
				t.Fatalf("packaging changed the extension:\n source %+v\n packed %+v\n inspected %+v", source, packed, back)
			}
			if name == "theme" && (len(source.Permissions) != 0 || source.Files != 1) {
				t.Fatalf("a theme is one manifest and needs no permission, got %+v", source)
			}
		})
	}
}
