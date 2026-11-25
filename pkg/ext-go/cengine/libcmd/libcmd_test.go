package libcmd

import (
	"context"
	"encoding/json"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"io"
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
	if err := run(context.Background(), io.Discard, []string{"fetch"}); err == nil || !strings.Contains(err.Error(), "-lib") {
		t.Errorf("no -lib: %v", err)
	}
	if err := run(context.Background(), io.Discard, []string{"fetch", "-lib", "demo"}); err == nil || !strings.Contains(err.Error(), "no citation") {
		t.Errorf("no citation: %v", err)
	}
	if err := run(context.Background(), io.Discard, []string{"build"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("unknown command: %v", err)
	}
}

func TestFetchStaysInsideTheModule(t *testing.T) {
	module(t, `{"libraries":{"demo":{"citation":"demo.citation.json","drivers":["cgo"],"targets":["linux/amd64"],"dir":"../outside"}}}`)
	if err := run(context.Background(), io.Discard, []string{"fetch", "-lib", "demo"}); err == nil || !strings.Contains(err.Error(), "outside the module") {
		t.Errorf("a directory outside the module: %v", err)
	}
}

func TestFetchReadsTheConfiguration(t *testing.T) {
	module(t, `{"dir":"third_party/c","libraries":{"demo":{"citation":"demo.citation.json","drivers":["cgo"],"targets":["linux/amd64"]}}}`)
	// The asset is unreachable, so the run fails at the download, after resolving the citation,
	// the drivers, the targets and the directory from ext-go.json.
	err := run(context.Background(), io.Discard, []string{"fetch", "-lib", "demo", "-offline"})
	if err == nil || !strings.Contains(err.Error(), "offline") || !strings.Contains(err.Error(), "example.invalid") {
		t.Errorf("offline fetch of a configured library: %v", err)
	}
	if err := run(context.Background(), io.Discard, []string{"fetch", "-lib", "demo", "-target", "windows/arm64", "-offline"}); err == nil || !strings.Contains(err.Error(), "windows/arm64") {
		t.Errorf("a target the flag names: %v", err)
	}
}

func TestBindWritesTheBindingOfADescription(t *testing.T) {
	description, err := filepath.Abs("../../../../lib/plugin/interface.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(description); err != nil {
		t.Skip("lib/plugin is not beside this module")
	}
	root := module(t, `{"libraries":{"ext_plugin":{"description":"`+filepath.ToSlash(description)+`"}}}`)
	if err := run(context.Background(), io.Discard, []string{"bind"}); err == nil || !strings.Contains(err.Error(), "-lib") {
		t.Errorf("no -lib: %v", err)
	}
	if err := run(context.Background(), io.Discard, []string{"bind", "-lib", "ext_other"}); err == nil || !strings.Contains(err.Error(), "no description") {
		t.Errorf("no description: %v", err)
	}
	if err := run(context.Background(), io.Discard, []string{"bind", "-lib", "ext_plugin"}); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "extlib", "pluginbind", "pluginbind.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "package pluginbind") || !strings.Contains(string(src), `Prefix     = "ext_plugin"`) {
		t.Errorf("the binding:\n%.400s", src)
	}
	if err := run(context.Background(), io.Discard, []string{"bind", "-lib", "ext_plugin", "-dir", "../outside"}); err == nil || !strings.Contains(err.Error(), "outside the module") {
		t.Errorf("a directory outside the module: %v", err)
	}
}

func TestFetchAllTargetsDeduplicatesDriverAssets(t *testing.T) {
	root := module(t, `{"libraries":{"demo":{"citation":"demo.citation.json","drivers":["cgo"]}}}`)
	path := filepath.Join(root, "demo.citation.json")
	c, err := library.ReadCitation(path)
	if err != nil {
		t.Fatal(err)
	}
	extra := c.Assets[0]
	extra.Drivers = library.Drivers{PureGo: &library.PureGoFiles{Shared: "lib/demo.so"}}
	c.Assets = append(c.Assets, extra)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), io.Discard, []string{"fetch", "-lib", "demo", "-target", "all", "-offline"})
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("all failed before download: %v", err)
	}
}
