package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/res/content"
)

func TestContentCatalogCLI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "content")
	source := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		if code := Run(append([]string{"content"}, args...), &out, &diagnostics); code != 0 {
			t.Fatalf("content %v = %d: %s", args, code, diagnostics.String())
		}
		return out.String()
	}
	run("init", root)
	var item content.Record
	if err := json.Unmarshal([]byte(run("import", root, source)), &item); err != nil || item.ID == "" {
		t.Fatalf("import item: %+v, %v", item, err)
	}
	if got := run("list", root); !strings.Contains(got, item.ID) {
		t.Fatalf("list = %s", got)
	}
	if got := run("verify", root, item.ID); !strings.Contains(got, item.SHA256) {
		t.Fatalf("verify = %s", got)
	}
	if got := run("inspect", root, item.ID); !strings.Contains(got, "note.txt") {
		t.Fatalf("inspect = %s", got)
	}
	run("remove", root, item.ID)
	if got := run("list", root); strings.TrimSpace(got) != "[]" {
		t.Fatalf("list after remove = %s", got)
	}
}
