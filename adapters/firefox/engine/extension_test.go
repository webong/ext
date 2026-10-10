package firefox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/extension"
)

func extensionFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"manifest_version":3,"name":"Example","version":"1.0","permissions":["storage"],"host_permissions":["https://example.com/*"]}`
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "background.js"), []byte("console.log('ready')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSigningUsesEnvironmentAndReturnsSignedArtifactRevision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := filepath.Join(t.TempDir(), "fixture.zip")
	if _, err := extension.Package(source, unsigned); err != nil {
		t.Fatal(err)
	}
	toolDir := t.TempDir()
	tool := filepath.Join(toolDir, "web-ext")
	if err := os.WriteFile(tool, []byte(`#!/bin/sh
set -eu
for arg in "$@"; do
  case "$arg" in --artifacts-dir=*) artifacts="${arg#*=}";; esac
done
test "$WEB_EXT_API_KEY" = fixture-key
test "$WEB_EXT_API_SECRET" = fixture-secret
cp "$MOCK_XPI" "$artifacts/signed.xpi"
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WEB_EXT_API_KEY", "fixture-key")
	t.Setenv("WEB_EXT_API_SECRET", "fixture-secret")
	t.Setenv("MOCK_XPI", unsigned)
	output := filepath.Join(t.TempDir(), "signed.xpi")
	result, err := SignFirefoxXPI(context.Background(), source, output, prepared.Revision)
	if err != nil {
		t.Fatal(err)
	}
	fromXPI, err := extension.Inspect(output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "signed" || result.ArtifactRevision != fromXPI.Revision || result.SourceRevision != prepared.Revision {
		t.Fatalf("incorrect XPI result: %+v", result)
	}
	t.Setenv("WEB_EXT_API_SECRET", "")
	if _, err := SignFirefoxXPI(context.Background(), source, filepath.Join(t.TempDir(), "missing.xpi"), prepared.Revision); err == nil || !strings.Contains(err.Error(), "WEB_EXT_API_SECRET") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}
}

func TestProfileVerification(t *testing.T) {
	source := extensionFixture(t)
	profile := t.TempDir()
	id := "example@example.test"
	archive := filepath.Join(profile, "extensions", id+".xpi")
	if _, err := extension.Package(source, archive); err != nil {
		t.Fatal(err)
	}
	description, err := extension.Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	write := func(addon map[string]any) {
		data, _ := json.Marshal(map[string]any{"addons": []any{addon}})
		if err := os.WriteFile(filepath.Join(profile, "extensions.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	active := map[string]any{
		"id": id, "version": description.Version, "type": "extension", "path": archive,
		"active": true, "userDisabled": false, "appDisabled": false,
		"defaultLocale": map[string]string{"name": description.Name},
	}
	write(active)
	if err := verifyFirefoxProfile(profile, id, description); err != nil {
		t.Fatal(err)
	}
	write(map[string]any{"id": id, "version": description.Version, "type": "extension", "path": archive, "active": false})
	if err := verifyFirefoxProfile(profile, id, description); err == nil {
		t.Fatal("accepted a disabled extension")
	}
	// An add-on installed outside the profile's extensions directory is not the
	// reviewed package, even when its metadata looks right.
	outside := filepath.Join(t.TempDir(), id+".xpi")
	if _, err := extension.Package(source, outside); err != nil {
		t.Fatal(err)
	}
	active["path"] = outside
	write(active)
	if err := verifyFirefoxProfile(profile, id, description); err == nil {
		t.Fatal("accepted a package outside the profile")
	}
	// A package whose content differs from the reviewed revision is rejected.
	active["path"] = archive
	write(active)
	other := description
	other.Revision = "sha256:other"
	if err := verifyFirefoxProfile(profile, id, other); err == nil {
		t.Fatal("accepted a different revision")
	}
}

func TestExtensionCapabilityFollowsConfig(t *testing.T) {
	disabled := extensionCapability(Config{Name: "example"})
	if disabled.PersistentLocalInstall || disabled.SessionLoad || disabled.Reason == "" {
		t.Fatalf("adapter without native extensions reports a route: %+v", disabled)
	}
	enabled := extensionCapability(Config{Name: "example", NativeExtensions: true})
	if !enabled.PersistentLocalInstall || !enabled.SessionLoad || enabled.InstallDriver != "webdriver-bidi-signed-xpi" || enabled.RequiresBrowserAction {
		t.Fatalf("incorrect capability: %+v", enabled)
	}
}

// Set EXT_TEST_FIREFOX_BIN to a Firefox-family executable to check that the
// browser rejects an unsigned permanent XPI through WebDriver BiDi.
func TestBiDiRejectsUnsignedPermanentXPI(t *testing.T) {
	executable := os.Getenv("EXT_TEST_FIREFOX_BIN")
	if executable == "" {
		t.Skip("set EXT_TEST_FIREFOX_BIN to test an installed Firefox-family browser")
	}
	target := extension.NewTarget("example", executable, filepath.Join(t.TempDir(), "profile"), "", "example")
	session, err := startFirefox(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	var status struct {
		Ready bool `json:"ready"`
	}
	if err := session.call(context.Background(), "session.status", map[string]any{}, &status); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "unsigned.xpi")
	if _, err := extension.Package(extensionFixture(t), archive); err != nil {
		t.Fatal(err)
	}
	var installed struct {
		Extension string `json:"extension"`
	}
	err = session.call(context.Background(), "webExtension.install", map[string]any{
		"extensionData": map[string]string{"type": "archivePath", "path": archive}, "moz:permanent": true,
	}, &installed)
	if err == nil || strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("browser did not reject an unsigned permanent XPI through BiDi: %v", err)
	}
	if err := session.close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
