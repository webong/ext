package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func TestBundleLifecycle(t *testing.T) {
	native := os.Getenv("EXT_ACCEPTANCE_LIBRARY")
	if native == "" {
		t.Skip("run scripts/package-acceptance.sh for required bundle acceptance")
	}
	home, bin, outside, files := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ext := filepath.Join(bin, "ext"+suffix)
	goBuild(t, ext, ".")
	stageAcceptanceEngine(t, bin, native)
	run := func(want int, contains string, args ...string) string {
		t.Helper()
		cmd := exec.Command(ext, args...)
		cmd.Dir = outside
		cmd.Env = acceptanceEnv(home)
		output, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want || !strings.Contains(string(output), contains) {
			t.Fatalf("%v: %d %s; want %d %q", args, code, output, want, contains)
		}
		return string(output)
	}
	ref := plugin.ContractRef{Name: "example.value", Version: "v1"}
	providerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(providerDir, "native-data"), []byte("native artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := packagekit.SourceManifest("example/provider", "1.0.0", []plugin.Contract{{ContractRef: ref, Operations: []plugin.Operation{{Name: "read"}}}}, providerDir, []string{"native-data"})
	if err != nil {
		t.Fatal(err)
	}
	commandDir := t.TempDir()
	guest := filepath.Join(commandDir, "hello"+suffix)
	goBuild(t, guest, "./testdata/guest")
	command, err := packagekit.ExecutableManifest("example/hello", "1.0.0", []plugin.Contract{pkgcmd.RunContract}, guest)
	if err != nil {
		t.Fatal(err)
	}
	command.Descriptor.Identity.Revision = "child"
	command.Requires = []packagekit.Dependency{{Contract: ref, Operation: "read"}}
	command.Metadata = map[string]json.RawMessage{pkgcmd.MetadataKey: json.RawMessage(`{"commands":[{"name":"hi"}]}`)}
	archive := func(dir, name string, m packagekit.Manifest) map[string]any {
		t.Helper()
		if err := store.WriteManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		paths := []string{store.ManifestName}
		for _, a := range m.Artifacts {
			paths = append(paths, a.Path)
		}
		for _, path := range paths {
			data, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			header := &zip.FileHeader{Name: path, Method: zip.Deflate}
			header.SetMode(0700)
			w, err := z.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(files, name), buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		return map[string]any{"manifest": m, "archive": map[string]string{"source": name, "sha256": fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))}}
	}
	p := archive(providerDir, "provider.zip", provider)
	c := archive(commandDir, "command.zip", command)
	bundle := map[string]any{"apiVersion": "ext.bundle/v1", "target": runtime.GOOS + "/" + runtime.GOARCH, "packages": []any{c, p}}
	file := filepath.Join(files, "bundle.json")
	write := func() {
		t.Helper()
		data, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	run(0, `"bindings"`, "bundle", "plan", file)
	run(0, "example/provider", "bundle", "inspect", file)
	pin := c["archive"].(map[string]string)["sha256"]
	c["archive"].(map[string]string)["sha256"] = strings.Repeat("0", 64)
	write()
	run(1, "prepare example/hello", "bundle", "install", file)
	if _, err := os.Stat(filepath.Join(home, "packages", "example%2fprovider")); !os.IsNotExist(err) {
		t.Fatalf("failed preparation published provider: %v", err)
	}
	c["archive"].(map[string]string)["sha256"] = pin
	// Correct archive pin cannot excuse a different pinned manifest.
	modified := command
	modified.License = "MIT"
	c["manifest"] = modified
	write()
	run(1, "manifest differs", "bundle", "install", file)
	c["manifest"] = command
	write()
	lock := filepath.Join(home, ".package-write.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	run(1, "package writer unavailable", "bundle", "install", file)
	run(1, "package writer unavailable", "package", "install", "unused")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	output := run(0, `"state":"complete"`, "bundle", "install", file)
	var progress struct {
		Digest    string   `json:"digest"`
		Completed []string `json:"completed"`
	}
	if err := json.Unmarshal([]byte(output), &progress); err != nil {
		t.Fatal(err)
	}
	if len(progress.Completed) != 2 || progress.Completed[0] != "example/provider" {
		t.Fatalf("dependency publication order: %+v", progress)
	}
	run(2, "unknown command", "hi")
	run(0, "trusted=false", "package", "verify", "example/provider")
	// Replaying is content-checked, leaves approvals alone, and needs no journal trust.
	run(0, `"state":"complete"`, "bundle", "install", file)
	run(0, "trusted", "package", "trust", "example/provider")
	run(0, "trusted", "package", "trust", "example/hello")
	run(0, "bundle-ready", "hi", "bundle-ready")
	if err := os.WriteFile(filepath.Join(home, "bundles", progress.Digest+".json"), []byte(`{"state":"partial"}`), 0600); err != nil {
		t.Fatal(err)
	}
	run(0, `"state":"complete"`, "bundle", "install", file)
	run(0, "still-approved", "hi", "still-approved")

	// Compose an actual native engine with the same pinned package selection.
	project := filepath.Join(home, "artifacts", "native", runtime.GOOS, runtime.GOARCH)
	nativeBytes, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	nativeName := filepath.Base(native)
	var nativeArchive bytes.Buffer
	gz := gzip.NewWriter(&nativeArchive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "lib/" + nativeName, Mode: 0644, Size: int64(len(nativeBytes))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(nativeBytes); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	nativePin := fmt.Sprintf("%x", sha256.Sum256(nativeArchive.Bytes()))
	cache := filepath.Join(home, "cache", "native")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, nativePin+".tar.gz"), nativeArchive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	citation := library.Citation{Schema: library.CitationSchema, Library: "ext_plugin", Version: "acceptance", Assets: []library.Asset{{Targets: []string{runtime.GOOS + "/" + runtime.GOARCH}, URL: "https://example.invalid/engine.tar.gz", SHA256: nativePin, Drivers: library.Drivers{PureGo: &library.PureGoFiles{Shared: "lib/" + nativeName}}}}}
	bundle["native"] = []any{map[string]any{"citation": citation, "drivers": []string{"purego"}, "dir": "internal/extlib/plugin"}}
	write()
	output = run(0, `"state":"complete"`, "bundle", "install", file)
	if err := json.Unmarshal([]byte(output), &progress); err != nil {
		t.Fatal(err)
	}
	if len(progress.Completed) != 3 || progress.Completed[0] != "native:ext_plugin" {
		t.Fatalf("mixed publication order: %+v", progress)
	}
	installedNative := filepath.Join(project, "internal", "extlib", "plugin", "lib", runtime.GOOS+"_"+runtime.GOARCH, nativeName)
	loaded, err := library.Open(installedNative)
	if err != nil {
		t.Fatal(err)
	}
	api, err := library.BindInterface(loaded, "ext_plugin", 1)
	if err != nil {
		t.Fatal(err)
	}
	described, err := library.Describe(api)
	if err != nil {
		t.Fatal(err)
	}
	var codeName string
	if err := described.Library().Call("code_name", map[string]int{"code": 0}, &codeName); err != nil || codeName != "ok" {
		t.Fatalf("installed native library: %s %v", codeName, err)
	}
	run(0, "mixed-bundle-ready", "hi", "mixed-bundle-ready")
	run(0, `"state":"complete"`, "bundle", "install", file)
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("bundle wrote into invoking directory: %v %v", entries, err)
	}
	delete(bundle, "native")
	write()
	// A changed bundle cannot silently overwrite a tampered installed package.
	if err := os.WriteFile(filepath.Join(home, "packages", "example%2fprovider", "native-data"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	run(1, "", "bundle", "install", file)
}
