package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(dir); err != nil || len(c.Sources) != 0 {
		t.Fatalf("missing file = %+v, %v", c, err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, File), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"sources":{"ctx":"package"}}`)
	if c, err := Load(dir); err != nil || c.Sources["ctx"] != "package" {
		t.Fatalf("config = %+v, %v", c, err)
	}
	for _, bad := range []string{`{"sources":`, `{"sourcez":{}}`} {
		write(bad)
		if _, err := Load(dir); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
