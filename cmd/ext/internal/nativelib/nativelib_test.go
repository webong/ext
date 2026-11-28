package nativelib

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

// setup isolates Resolve: an executable in an empty directory, a private cache, no citations and no
// library variables.
func setup(t *testing.T) (exeDir string) {
	t.Helper()
	exeDir = t.TempDir()
	saved := struct {
		citations  func() (map[string]library.Citation, error)
		cache      string
		download   func(context.Context, string) (io.ReadCloser, error)
		notice     io.Writer
		executable func() (string, error)
	}{citations, cacheDir, download, notice, executable}
	t.Cleanup(func() {
		citations, cacheDir, download, notice, executable = saved.citations, saved.cache, saved.download, saved.notice, saved.executable
		resolved = map[string]result{}
	})
	resolved = map[string]result{}
	citations = func() (map[string]library.Citation, error) { return nil, nil }
	cacheDir = t.TempDir()
	notice = io.Discard
	executable = func() (string, error) { return filepath.Join(exeDir, "ext"), nil }
	for _, v := range []string{Plugin.Env, Ledger.Env, "EXT_OFFLINE"} {
		t.Setenv(v, "")
	}
	return exeDir
}

func write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// release publishes an archive holding the plugin library for this platform, cites it and makes it
// the one download reaches, counting the downloads.
func release(t *testing.T) *int {
	t.Helper()
	shared := "lib/" + Plugin.File(runtime.GOOS)
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	body := []byte("engine")
	tw.WriteHeader(&tar.Header{Name: "ext-plugin/" + shared, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	data := b.Bytes()
	sum := sha256.Sum256(data)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	t.Cleanup(srv.Close)
	n := new(int)
	download = func(ctx context.Context, url string) (io.ReadCloser, error) {
		*n++
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := srv.Client().Do(req)
		if err != nil {
			return nil, err
		}
		return resp.Body, nil
	}
	citation := library.Citation{Schema: library.CitationSchema, Library: Plugin.Name, Version: "9.9.9", Assets: []library.Asset{{
		Targets: []string{runtime.GOOS + "/" + runtime.GOARCH}, URL: srv.URL + "/ext-plugin.tar.gz", SHA256: hex.EncodeToString(sum[:]),
		Root: "ext-plugin", Drivers: library.Drivers{PureGo: &library.PureGoFiles{Shared: shared}},
	}}}
	citations = func() (map[string]library.Citation, error) {
		return map[string]library.Citation{Plugin.Name: citation}, nil
	}
	return n
}

func TestVariableIsFinal(t *testing.T) {
	exeDir := setup(t)
	write(t, filepath.Join(exeDir, "ext-lib", Plugin.File(runtime.GOOS)), "bundled")
	t.Setenv(Plugin.Env, "/chosen/engine")
	if path, err := Resolve(Plugin); err != nil || path != "/chosen/engine" {
		t.Fatalf("%s, %v", path, err)
	}
}

func TestBundled(t *testing.T) {
	exeDir := setup(t)
	want := write(t, filepath.Join(exeDir, "ext-lib", Plugin.File(runtime.GOOS)), "bundled")
	if path, err := Resolve(Plugin); err != nil || path != want {
		t.Fatalf("%s, %v", path, err)
	}
}

func TestLedgerBesidePlugin(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	t.Setenv(Plugin.Env, write(t, filepath.Join(dir, Plugin.File(runtime.GOOS)), "engine"))
	want := write(t, filepath.Join(dir, Ledger.File(runtime.GOOS)), "ledger")
	if path, err := Resolve(Ledger); err != nil || path != want {
		t.Fatalf("%s, %v", path, err)
	}
}

func TestDownloadsThenKeeps(t *testing.T) {
	setup(t)
	downloads := release(t)
	path, err := Resolve(Plugin)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "engine" || !strings.HasPrefix(path, cacheDir) {
		t.Fatalf("%s holds %q", path, data)
	}
	// A new process, offline, uses the kept copy.
	resolved = map[string]result{}
	t.Setenv("EXT_OFFLINE", "1")
	if again, err := Resolve(Plugin); err != nil || again != path {
		t.Fatalf("%s, %v", again, err)
	}
	if *downloads != 1 {
		t.Errorf("%d downloads, want 1", *downloads)
	}
	if env := strings.Join(Environment(), "\n"); !strings.Contains(env, Plugin.Env+"="+path) {
		t.Errorf("environment %q lacks the plugin", env)
	}
}

func TestOfflineWithNothingKept(t *testing.T) {
	setup(t)
	downloads := release(t)
	t.Setenv("EXT_OFFLINE", "1")
	_, err := Resolve(Plugin)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "EXT_OFFLINE") || *downloads != 0 {
		t.Fatalf("%v after %d downloads", err, *downloads)
	}
}

func TestNoCitation(t *testing.T) {
	setup(t)
	_, err := Resolve(Ledger)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), Ledger.Env) || !strings.Contains(err.Error(), "ext-lib") {
		t.Fatalf("%v", err)
	}
}

func TestEmbeddedCitationsParse(t *testing.T) {
	cited, err := loadCitations()
	if err != nil {
		t.Fatal(err)
	}
	for name := range cited {
		if name != Plugin.Name && name != Ledger.Name {
			t.Errorf("embedded citation for %s, which ext does not run on", name)
		}
	}
}
