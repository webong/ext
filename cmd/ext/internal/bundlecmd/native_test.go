package bundlecmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeFixture(t *testing.T) Bundle {
	t.Helper()
	citation := json.RawMessage(`{"schema":"ext.library.citation/v1","library":"ext_demo","version":"1.0.0","assets":[{"targets":["linux/amd64"],"url":"https://example.invalid/native.tar.gz","sha256":"` + strings.Repeat("a", 64) + `","drivers":{"purego":{"shared":"lib/demo.so"}}}]}`)
	return Bundle{APIVersion: "ext.bundle/v1", Target: "linux/amd64", Packages: []Item{}, Native: []NativeJob{{Citation: citation, Drivers: []string{"purego"}, Dir: "internal/extlib/demo"}}}
}

func TestNativePlanSelection(t *testing.T) {
	b := nativeFixture(t)
	plan, err := planNative(b)
	if err != nil || len(plan) != 1 || plan[0].Library != "ext_demo" || plan[0].Package != "demo" || plan[0].Selections[0].Asset.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	for _, name := range []string{"missing-target", "missing-driver", "duplicate-library", "overlap", "keyword"} {
		t.Run(name, func(t *testing.T) {
			b := nativeFixture(t)
			switch name {
			case "missing-target":
				b.Target = "windows/arm64"
			case "missing-driver":
				b.Native[0].Drivers = []string{"cgo"}
			case "duplicate-library":
				b.Native = append(b.Native, b.Native[0])
			case "overlap":
				other := b.Native[0]
				other.Citation = json.RawMessage(strings.ReplaceAll(string(other.Citation), "ext_demo", "ext_other"))
				other.Dir = "internal/extlib/DEMO/child"
				b.Native = append(b.Native, other)
			case "keyword":
				b.Native[0].Package = "type"
			}
			if _, err := planNative(b); err == nil {
				t.Fatal("invalid native plan accepted")
			}
		})
	}
}

func TestNativeBundlePlanDoesNotFetch(t *testing.T) {
	lib := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if lib == "" {
		t.Skip("run scripts/package-acceptance.sh for C bundle planning acceptance")
	}
	t.Setenv("EXT_PLUGIN_LIBRARY", lib)
	b := nativeFixture(t)
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	m := Manager{Home: home}
	var out, stderr bytes.Buffer
	if code := m.Run([]string{"plan", file}, &out, &stderr); code != 0 {
		t.Fatalf("plan: %d %s", code, stderr.String())
	}
	if !strings.Contains(out.String(), `"library":"ext_demo"`) || !strings.Contains(out.String(), `"target":"linux/amd64"`) {
		t.Fatalf("missing native selection: %s", out.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("plan created home: %v", err)
	}
	if code := m.Run([]string{"install", file, "--project", t.TempDir()}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("obsolete project option was accepted: %d %s", code, stderr.String())
	}
}
