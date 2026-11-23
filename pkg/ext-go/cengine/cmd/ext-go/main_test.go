package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// module makes a scratch module with an ext-go.json and a citation, and enters it.
func module(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":      "module example.test/app\n\ngo 1.25.0\n",
		"ext-go.json": config,
		"demo.citation.json": `{"schema":"ext.library.citation/v1","library":"ext_demo","version":"1.0.0","assets":[{"targets":["linux/amd64"],
			"url":"https://example.invalid/demo.tar.gz","sha256":"` + strings.Repeat("a", 64) + `","drivers":{"cgo":{"include":"include","static":"lib/libext_demo.a"}}}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	return dir
}

func TestFetchNeedsALibraryAndACitation(t *testing.T) {
	module(t, `{}`)
	if err := run(context.Background(), []string{"fetch"}); err == nil || !strings.Contains(err.Error(), "-lib") {
		t.Errorf("no -lib: %v", err)
	}
	if err := run(context.Background(), []string{"fetch", "-lib", "demo"}); err == nil || !strings.Contains(err.Error(), "no citation") {
		t.Errorf("no citation: %v", err)
	}
	if err := run(context.Background(), []string{"build"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("unknown command: %v", err)
	}
}

func TestFetchStaysInsideTheModule(t *testing.T) {
	module(t, `{"libraries":{"demo":{"citation":"demo.citation.json","drivers":["cgo"],"targets":["linux/amd64"],"dir":"../outside"}}}`)
	if err := run(context.Background(), []string{"fetch", "-lib", "demo"}); err == nil || !strings.Contains(err.Error(), "outside the module") {
		t.Errorf("a directory outside the module: %v", err)
	}
}

func TestFetchReadsTheConfiguration(t *testing.T) {
	module(t, `{"dir":"third_party/c","libraries":{"demo":{"citation":"demo.citation.json","drivers":["cgo"],"targets":["linux/amd64"]}}}`)
	// The asset is unreachable, so the run fails at the download, after resolving the citation,
	// the drivers, the targets and the directory from ext-go.json.
	err := run(context.Background(), []string{"fetch", "-lib", "demo", "-offline"})
	if err == nil || !strings.Contains(err.Error(), "offline") || !strings.Contains(err.Error(), "example.invalid") {
		t.Errorf("offline fetch of a configured library: %v", err)
	}
	if err := run(context.Background(), []string{"fetch", "-lib", "demo", "-target", "windows/arm64", "-offline"}); err == nil || !strings.Contains(err.Error(), "windows/arm64") {
		t.Errorf("a target the flag names: %v", err)
	}
}
