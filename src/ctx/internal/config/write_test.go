package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicFlatMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ctx")
	if err := SetFlat(path, "docker", "orbstack"); err != nil {
		t.Fatal(err)
	}
	if err := SetFlat(path, "browser", "chrome:Profile 1"); err != nil {
		t.Fatal(err)
	}
	if err := SetFlat(path, "docker", "desktop-linux"); err != nil {
		t.Fatal(err)
	}
	values, err := ReadFlat(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["docker"] != "desktop-linux" || values["browser"] != "chrome:Profile 1" {
		t.Fatalf("unexpected values: %#v", values)
	}
	if err := RemoveFlat(path, "docker"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFlat(path, "browser"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty .ctx was not removed: %v", err)
	}
}

func TestSectionMutationsPreserveOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, path, "# keep me\n\n[projects.\"/project\"]\ndocker = \"remote\"\n")
	if err := SetRoot(path, "docker_default", "orbstack"); err != nil {
		t.Fatal(err)
	}
	if err := SetSection(path, `[profiles."client"]`, "browser", "firefox:default"); err != nil {
		t.Fatal(err)
	}
	if err := SetSection(path, `[profiles."client".env]`, "APP_ENV", "development"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSectionValue(path, `[profiles."client"]`, "browser"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, expected := range []string{"# keep me", `docker_default = "orbstack"`, `[projects."/project"]`, `APP_ENV = "development"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in:\n%s", expected, text)
		}
	}
}
