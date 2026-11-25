package bundlecmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func TestNativeBundleInstallAndReplay(t *testing.T) {
	lib := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if lib == "" {
		t.Skip("run scripts/package-acceptance.sh for native bundle acceptance")
	}
	t.Setenv("EXT_PLUGIN_LIBRARY", lib)
	home := t.TempDir()
	project := filepath.Join(home, "artifacts", "native", "linux", "amd64")
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	body := []byte("pinned native fixture")
	if err := tw.WriteHeader(&tar.Header{Name: "lib/demo.so", Mode: 0644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(archive.Bytes())
	b := nativeFixture(t)
	var citation library.Citation
	if err := json.Unmarshal(b.Native[0].Citation, &citation); err != nil {
		t.Fatal(err)
	}
	citation.Assets[0].SHA256 = hex.EncodeToString(pin[:])
	data, err := json.Marshal(citation)
	if err != nil {
		t.Fatal(err)
	}
	b.Native[0].Citation = data
	cache := filepath.Join(home, "cache", "native")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, citation.Assets[0].SHA256+".tar.gz"), archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := Manager{Home: home}
	run := func(want int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		code := m.Run(args, &out, &stderr)
		if code != want {
			t.Fatalf("%v: %d %s", args, code, stderr.String())
		}
		return out.String() + stderr.String()
	}
	// A later native preparation failure must not publish the first output.
	other := b.Native[0]
	otherCitation := citation
	otherCitation.Library = "ext_other"
	otherCitation.Assets = append([]library.Asset(nil), citation.Assets...)
	otherCitation.Assets[0].Drivers = library.Drivers{PureGo: &library.PureGoFiles{Shared: "lib/missing.so"}}
	other.Citation, _ = json.Marshal(otherCitation)
	other.Dir = "internal/extlib/other"
	broken := b
	broken.Native = append(append([]NativeJob(nil), b.Native...), other)
	brokenData, _ := json.Marshal(broken)
	brokenFile := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(brokenFile, brokenData, 0600); err != nil {
		t.Fatal(err)
	}
	if out := run(1, "install", brokenFile); !strings.Contains(out, "missing.so") {
		t.Fatalf("preparation failure: %s", out)
	}
	if _, err := os.Stat(filepath.Join(project, "internal")); !os.IsNotExist(err) {
		t.Fatalf("preparation published output: %v", err)
	}
	run(0, "install", file)
	native := filepath.Join(project, "internal", "extlib", "demo", "lib", "linux_amd64", "demo.so")
	got, err := os.ReadFile(native)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("native output: %q %v", got, err)
	}
	run(0, "install", file)
	// A new citation version changes generated files and upgrades only the
	// unchanged output covered by the previous receipt.
	citation.Version = "2.0.0"
	b.Native[0].Citation, _ = json.Marshal(citation)
	updated, _ := json.Marshal(b)
	if err := os.WriteFile(file, updated, 0600); err != nil {
		t.Fatal(err)
	}
	run(0, "install", file)
	generated, err := os.ReadFile(filepath.Join(project, "internal", "extlib", "demo", "doc.go"))
	if err != nil || !strings.Contains(string(generated), "2.0.0") {
		t.Fatalf("upgrade: %s %v", generated, err)
	}
	if err := os.WriteFile(native, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if out := run(1, "install", file); !strings.Contains(out, "different content") {
		t.Fatalf("tampering: %s", out)
	}
	got, err = os.ReadFile(native)
	if err != nil || string(got) != "user edit" {
		t.Fatalf("overwrote user content: %q %v", got, err)
	}
}

func TestNativeDestinationBoundaries(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, path := range []string{"../escape", "/absolute", ".git/output", "linked/output"} {
		if _, err := nativeDestination(root, path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}

func TestNativeArtifactRoot(t *testing.T) {
	home := t.TempDir()
	linux, err := nativeArtifactRoot(home, "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	windows, err := nativeArtifactRoot(home, "windows/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if linux == windows {
		t.Fatal("targets share artifact root")
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if linux != filepath.Join(canonical, "artifacts", "native", "linux", "amd64") {
		t.Fatalf("wrong artifact root %s", linux)
	}
	if _, err := os.Stat(filepath.Join(home, "go.mod")); !os.IsNotExist(err) {
		t.Fatalf("created Go project: %v", err)
	}
	if _, err := nativeArtifactRoot(home, "../../outside"); err == nil {
		t.Fatal("accepted escaping target")
	}
	linked := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(linked, "artifacts")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := nativeArtifactRoot(linked, "linux/amd64"); err == nil {
		t.Fatal("followed artifact symlink")
	}
}
