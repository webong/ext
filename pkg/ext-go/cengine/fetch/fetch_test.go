package fetch_test

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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

type entry struct {
	name, body string
	link       bool
}

func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link {
			h = &tar.Header{Name: e.name, Linkname: e.body, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if !e.link {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// serve publishes files over HTTPS and returns a Fetch that reaches them.
func serve(t *testing.T, files map[string][]byte) (string, func(context.Context, string) (io.ReadCloser, error)) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	client := srv.Client()
	return srv.URL, func(ctx context.Context, url string) (io.ReadCloser, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, errors.New(resp.Status)
		}
		return resp.Body, nil
	}
}

func demoCitation(base, sum string, targets ...string) library.Citation {
	return library.Citation{Schema: library.CitationSchema, Library: "ext_demo", Version: "1.0.0", Assets: []library.Asset{{
		Targets: targets, URL: base + "/demo.tar.gz", SHA256: sum, Root: "demo",
		Drivers: library.Drivers{
			CGo:    &library.CGoFiles{Include: "include", Static: "lib/libext_demo_static.a", CFlags: []string{"-DEXT_DEMO_STATIC"}, LDFlags: []string{"-lm"}},
			PureGo: &library.PureGoFiles{Shared: "lib/libext_demo.so"},
			C2Go:   &library.C2GoFiles{Go: "c2go"},
		},
	}}}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLaysOutEveryDriver(t *testing.T) {
	data := archive(t,
		entry{name: "demo/include/ext_demo.h", body: "int ext_demo_answer(void);\n"},
		entry{name: "demo/lib/libext_demo_static.a", body: "static"},
		entry{name: "demo/lib/libext_demo.so", body: "shared"},
		entry{name: "demo/lib/libext_demo.so.1", body: "libext_demo.so", link: true},
		entry{name: "demo/c2go/generated_linux_amd64.go", body: "package demo\n"},
		entry{name: "demo/c2go/binding.go", body: "package demo\n"},
		entry{name: "demo/README.md", body: "ignored"},
	)
	base, get := serve(t, map[string][]byte{"/demo.tar.gz": data})
	dir := filepath.Join(t.TempDir(), "demo")
	o := fetch.Options{Citation: demoCitation(base, digestOf(data), "linux/amd64", "linux/arm64"), Drivers: []string{fetch.CGo, fetch.PureGo, fetch.C2Go},
		Targets: []string{"linux/amd64", "linux/arm64"}, Dir: dir, Fetch: get, Cache: t.TempDir()}
	if err := fetch.Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "include/ext_demo.h"); !strings.Contains(got, "ext_demo_answer") {
		t.Errorf("header: %q", got)
	}
	cgo := read(t, dir, "cgo_linux_amd64.go")
	for _, want := range []string{"//go:build cgo && linux && amd64", "package demo", "#cgo CFLAGS: -I${SRCDIR}/include -DEXT_DEMO_STATIC", "#cgo LDFLAGS: ${SRCDIR}/lib/linux_amd64/libext_demo_static.a -lm", `import "C"`} {
		if !strings.Contains(cgo, want) {
			t.Errorf("cgo file lacks %q:\n%s", want, cgo)
		}
	}
	if read(t, dir, "lib/linux_arm64/libext_demo_static.a") != "static" || read(t, dir, "lib/linux_amd64/libext_demo.so") != "shared" {
		t.Error("libraries not laid out per target")
	}
	pure := read(t, dir, "purego_linux_arm64.go")
	for _, want := range []string{"//go:build linux && arm64", "//go:embed lib/linux_arm64/libext_demo.so", `library.Provide("ext_demo"`, digestOf([]byte("shared"))} {
		if !strings.Contains(pure, want) {
			t.Errorf("purego file lacks %q:\n%s", want, pure)
		}
	}
	if read(t, dir, "generated_linux_amd64.go") != "package demo\n" || read(t, dir, "binding.go") != "package demo\n" {
		t.Error("c2go source not copied")
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Error("a file no driver asked for was written")
	}

	// A second fetch, for one target and driver, removes what the first wrote and nothing else.
	if err := os.WriteFile(filepath.Join(dir, "mine.go"), []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.Drivers, o.Targets = []string{fetch.CGo}, []string{"linux/amd64"}
	if err := fetch.Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"purego_linux_arm64.go", "generated_linux_amd64.go", "lib/linux_arm64", "cgo_linux_arm64.go"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived a refetch", gone)
		}
	}
	if read(t, dir, "mine.go") == "" {
		t.Error("the project's own file was removed")
	}
}

func TestRefusesWhatTheCitationDoesNotPin(t *testing.T) {
	good := archive(t, entry{name: "demo/include/ext_demo.h", body: "x"}, entry{name: "demo/lib/libext_demo_static.a", body: "a"})
	base, get := serve(t, map[string][]byte{"/demo.tar.gz": good})
	run := func(c library.Citation, target string) error {
		return fetch.Fetch(context.Background(), fetch.Options{Citation: c, Drivers: []string{fetch.CGo}, Targets: []string{target}, Dir: t.TempDir(), Fetch: get, Cache: t.TempDir()})
	}
	if err := run(demoCitation(base, strings.Repeat("0", 64), "linux/amd64"), "linux/amd64"); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("a wrong digest: %v", err)
	}
	if err := run(demoCitation(base, digestOf(good), "linux/amd64"), "windows/arm64"); !errors.Is(err, library.ErrNoAsset) {
		t.Errorf("an unpublished target: %v", err)
	}
	c := demoCitation(base, digestOf(good), "linux/amd64")
	c.Assets[0].Drivers.CGo = nil
	if err := run(c, "linux/amd64"); err == nil || !strings.Contains(err.Error(), "no cgo build") {
		t.Errorf("an unpublished driver: %v", err)
	}
	escape := archive(t, entry{name: "../escape", body: "x"})
	base2, get2 := serve(t, map[string][]byte{"/demo.tar.gz": escape})
	err := fetch.Fetch(context.Background(), fetch.Options{Citation: demoCitation(base2, digestOf(escape), "linux/amd64"), Drivers: []string{fetch.CGo},
		Targets: []string{"linux/amd64"}, Dir: t.TempDir(), Fetch: get2, Cache: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "leaves the archive") {
		t.Errorf("a path that leaves the archive: %v", err)
	}
}

func TestOfflineUsesTheCache(t *testing.T) {
	data := archive(t, entry{name: "demo/include/ext_demo.h", body: "x"}, entry{name: "demo/lib/libext_demo_static.a", body: "a"})
	base, get := serve(t, map[string][]byte{"/demo.tar.gz": data})
	cache := t.TempDir()
	o := fetch.Options{Citation: demoCitation(base, digestOf(data), "linux/amd64"), Drivers: []string{fetch.CGo}, Targets: []string{"linux/amd64"}, Cache: cache, Offline: true, Fetch: get}
	o.Dir = t.TempDir()
	if err := fetch.Fetch(context.Background(), o); !errors.Is(err, fetch.ErrOffline) {
		t.Fatalf("offline with an empty cache: %v", err)
	}
	o.Offline = false
	if err := fetch.Fetch(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	o.Offline, o.Fetch = true, func(context.Context, string) (io.ReadCloser, error) { return nil, errors.New("no network") }
	if err := fetch.Fetch(context.Background(), o); err != nil {
		t.Errorf("offline with the archive cached: %v", err)
	}
}

// TestDriversBuildAndRun builds a small C library, publishes it, fetches it into a scratch module and
// runs that module with the cgo driver and with the purego driver.
func TestDriversBuildAndRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a C library and two Go programs")
	}
	cc, err := exec.LookPath("cc")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs cc on a POSIX system")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "demo.c"), []byte("int ext_demo_answer(void) { return 42; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shared := "libext_demo.so"
	sharedFlag := "-shared"
	if runtime.GOOS == "darwin" {
		shared, sharedFlag = "libext_demo.dylib", "-dynamiclib"
	}
	for _, args := range [][]string{
		{"-c", "-fPIC", "-o", "demo.o", "demo.c"},
		{sharedFlag, "-o", shared, "demo.o"},
	} {
		cmd := exec.Command(cc, args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cc %v: %v\n%s", args, err, out)
		}
	}
	ar := exec.Command("ar", "rcs", "libext_demo_static.a", "demo.o")
	ar.Dir = src
	if out, err := ar.CombinedOutput(); err != nil {
		t.Fatalf("ar: %v\n%s", err, out)
	}
	file := func(name string) string {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	data := archive(t,
		entry{name: "demo/include/ext_demo.h", body: "int ext_demo_answer(void);\n"},
		entry{name: "demo/lib/libext_demo_static.a", body: file("libext_demo_static.a")},
		entry{name: "demo/lib/" + shared, body: file(shared)},
	)
	base, get := serve(t, map[string][]byte{"/demo.tar.gz": data})
	target := runtime.GOOS + "/" + runtime.GOARCH
	c := demoCitation(base, digestOf(data), target)
	c.Assets[0].Drivers.C2Go = nil
	c.Assets[0].Drivers.CGo.CFlags, c.Assets[0].Drivers.CGo.LDFlags = nil, nil
	c.Assets[0].Drivers.PureGo.Shared = "lib/" + shared

	cengine, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		driver, main string
		cgo          string
	}{
		{fetch.CGo, `package main

// int ext_demo_answer(void);
import "C"

import (
	"fmt"

	_ "example.test/app/internal/extlib/demo"
)

func main() { fmt.Println(int(C.ext_demo_answer())) }
`, "1"},
		{fetch.PureGo, `package main

import (
	"fmt"
	"os"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	_ "example.test/app/internal/extlib/demo"
)

func main() {
	lib, err := library.OpenProvided("ext_demo", os.Getenv("DEMO_CACHE"))
	if err != nil {
		panic(err)
	}
	var answer func() int32
	if err := lib.Bind(&answer, "ext_demo_answer"); err != nil {
		panic(err)
	}
	fmt.Println(answer())
}
`, "0"},
	} {
		t.Run(d.driver, func(t *testing.T) {
			app := t.TempDir()
			gomod := "module example.test/app\n\ngo 1.25.0\n\nrequire github.com/webong/ext/pkg/ext-go/cengine v0.1.0\n\nreplace github.com/webong/ext/pkg/ext-go/cengine => " + cengine + "\n"
			if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(gomod), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(app, "main.go"), []byte(d.main), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := fetch.Fetch(context.Background(), fetch.Options{Citation: c, Drivers: []string{d.driver}, Targets: []string{target},
				Dir: filepath.Join(app, "internal", "extlib", "demo"), Fetch: get, Cache: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			sum, _ := os.ReadFile(filepath.Join(cengine, "go.sum"))
			if err := os.WriteFile(filepath.Join(app, "go.sum"), sum, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "run", ".")
			cmd.Dir = app
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "CGO_ENABLED="+d.cgo, "DEMO_CACHE="+t.TempDir())
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, out)
			}
			if strings.TrimSpace(string(out)) != "42" {
				t.Errorf("the program printed %q, want 42", out)
			}
		})
	}
}
