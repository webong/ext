package fetch_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/webong/ext/pkg/ext-go/cengine/fetch"
)

func sharedArchive(t *testing.T) []byte {
	return archive(t,
		entry{name: "demo/include/ext_demo.h", body: "x"},
		entry{name: "demo/lib/libext_demo.so", body: "shared"},
	)
}

// counting wraps a Fetch and counts the downloads it makes.
func counting(get func(context.Context, string) (io.ReadCloser, error)) (func(context.Context, string) (io.ReadCloser, error), *atomic.Int32) {
	var n atomic.Int32
	return func(ctx context.Context, url string) (io.ReadCloser, error) {
		n.Add(1)
		return get(ctx, url)
	}, &n
}

func TestSharedFetchesOnceAndHeals(t *testing.T) {
	data := sharedArchive(t)
	base, serveGet := serve(t, map[string][]byte{"/demo.tar.gz": data})
	get, downloads := counting(serveGet)
	dir, cache := t.TempDir(), t.TempDir()
	o := fetch.SharedOptions{Citation: demoCitation(base, digestOf(data), "linux/amd64"), Target: "linux/amd64", Dir: dir, Cache: cache, Fetch: get}

	path, err := fetch.Shared(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "ext_demo", digestOf(data), "libext_demo.so"); path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	if read(t, filepath.Dir(path), "libext_demo.so") != "shared" {
		t.Fatal("wrong contents")
	}

	// A kept copy is used with no download, even offline.
	o.Offline = true
	if again, err := fetch.Shared(context.Background(), o); err != nil || again != path {
		t.Fatalf("kept copy: %s, %v", again, err)
	}

	// A damaged copy is extracted again from the archive cache, still offline.
	if err := os.WriteFile(path, []byte("damaged"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch.Shared(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Dir(path), "libext_demo.so") != "shared" {
		t.Fatal("damaged copy not repaired")
	}

	// With neither copy nor archive, it downloads again.
	o.Offline = false
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".sha256"); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch.Shared(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if n := downloads.Load(); n != 2 {
		t.Errorf("%d downloads, want 2", n)
	}
}

func TestSharedRefuses(t *testing.T) {
	data := sharedArchive(t)
	base, get := serve(t, map[string][]byte{"/demo.tar.gz": data})
	ctx := context.Background()

	wrong := fetch.SharedOptions{Citation: demoCitation(base, strings.Repeat("0", 64), "linux/amd64"), Target: "linux/amd64", Dir: t.TempDir(), Cache: t.TempDir(), Fetch: get}
	if _, err := fetch.Shared(ctx, wrong); err == nil || !strings.Contains(err.Error(), "the citation pins") {
		t.Errorf("unpinned archive: %v", err)
	}

	offline := fetch.SharedOptions{Citation: demoCitation(base, digestOf(data), "linux/amd64"), Target: "linux/amd64", Dir: t.TempDir(), Cache: t.TempDir(), Offline: true, Fetch: get}
	if _, err := fetch.Shared(ctx, offline); !errors.Is(err, fetch.ErrOffline) {
		t.Errorf("offline: %v", err)
	}

	other := fetch.SharedOptions{Citation: demoCitation(base, digestOf(data), "linux/amd64"), Target: "windows/arm64", Dir: t.TempDir(), Fetch: get}
	if _, err := fetch.Shared(ctx, other); err == nil {
		t.Error("a target the citation does not publish was fetched")
	}
}

func TestSharedConcurrent(t *testing.T) {
	data := sharedArchive(t)
	base, get := serve(t, map[string][]byte{"/demo.tar.gz": data})
	dir, cache := t.TempDir(), t.TempDir()
	o := fetch.SharedOptions{Citation: demoCitation(base, digestOf(data), "linux/amd64"), Target: "linux/amd64", Dir: dir, Cache: cache, Fetch: get}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	paths := make([]string, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths[i], errs[i] = fetch.Shared(context.Background(), o)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
		if paths[i] != paths[0] {
			t.Fatalf("paths differ: %s, %s", paths[i], paths[0])
		}
	}
	if read(t, filepath.Dir(paths[0]), "libext_demo.so") != "shared" {
		t.Fatal("wrong contents")
	}
}
