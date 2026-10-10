package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func TestPackageSafariProjectWithXcode(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("EXT_TEST_SAFARI_PACKAGER") == "" {
		t.Skip("set EXT_TEST_SAFARI_PACKAGER=1 to run the installed Xcode converter")
	}
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "safari-project")
	result, err := PackageSafariProject(context.Background(), source, project, prepared.Revision, "test.example.extension", "Test Extension")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "packaged" {
		t.Fatalf("unexpected Safari package result: %+v", result)
	}
	entries, err := os.ReadDir(project)
	if err != nil || len(entries) == 0 {
		t.Fatalf("Safari converter did not create a project at %s: %v", project, err)
	}
}

func TestInspectSignedSafariApp(t *testing.T) {
	app := os.Getenv("EXT_TEST_SAFARI_APP")
	if runtime.GOOS != "darwin" || app == "" {
		t.Skip("set EXT_TEST_SAFARI_APP to inspect an existing signed Safari extension app")
	}
	description, err := InspectSafariApp(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	if description.ID == "" || description.Revision == "" {
		t.Fatalf("missing Safari app identity: %+v", description)
	}
}

func TestSafariRejectsInvalidInputs(t *testing.T) {
	source := extensionFixture(t)
	prepared, err := extension.Inspect(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PackageSafariProject(context.Background(), source, filepath.Join(t.TempDir(), "p"), "", "test.example.extension", "Test"); err == nil {
		t.Fatal("packaged without a reviewed revision")
	}
	if _, err := PackageSafariProject(context.Background(), source, filepath.Join(t.TempDir(), "p"), prepared.Revision, "not a bundle id", "Test"); err == nil && runtime.GOOS == "darwin" {
		t.Fatal("accepted an invalid bundle identifier")
	}
	if _, err := InstallSafariApp(context.Background(), filepath.Join(t.TempDir(), "Missing.app"), prepared.Revision); err == nil {
		t.Fatal("installed a missing app")
	}
}
