package cshared_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest"
	"github.com/webong/ext/pkg/ext-go/runtime/cshared"
)

// guest returns the Go c-shared conformance guest: EXT_CSHARED_GUEST, or one
// built from testdata/guest. Building it needs cgo even when this test binary
// is built without it, which is the point: the cgo-free loader runs a guest the
// C toolchain built. EXT_CSHARED_REQUIRED=1 turns a skip into a failure.
func guest(t *testing.T) string {
	t.Helper()
	skip := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("EXT_CSHARED_REQUIRED") == "1" {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}
	if !cshared.Supported() {
		skip("this build has no C ABI loader")
	}
	if path := os.Getenv("EXT_CSHARED_GUEST"); path != "" {
		return path
	}
	extension := map[string]string{"darwin": "dylib", "windows": "dll"}[runtime.GOOS]
	if extension == "" {
		extension = "so"
	}
	built.once.Do(func() {
		// Not t.TempDir: the image stays loaded, and Windows cannot delete a
		// loaded DLL. TestMain removes the directory, as far as it can.
		dir, err := os.MkdirTemp("", "cshared-guest")
		if err != nil {
			built.err = err
			return
		}
		buildDir = dir
		built.path = filepath.Join(dir, "guest."+extension)
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", built.path, "./testdata/guest")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			built.err = fmt.Errorf("%v\n%s", err, out)
		}
	})
	if built.err != nil {
		skip("build the c-shared guest (set EXT_CSHARED_GUEST to use a prebuilt one): %v", built.err)
	}
	return built.path
}

var (
	built struct {
		once sync.Once
		path string
		err  error
	}
	buildDir string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// goGuestRefused reports whether this platform refuses a Go c-shared guest:
// on darwin/amd64 two Go runtimes share one TLS slot for g.
func goGuestRefused(t *testing.T, path string) bool {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "amd64" {
		return false
	}
	_, err := cshared.Open(context.Background(), path)
	if !errors.Is(err, plugin.ErrUnsupported) || !strings.Contains(err.Error(), "darwin/amd64") {
		t.Fatalf("a Go guest on darwin/amd64: got %v, want ErrUnsupported naming darwin/amd64", err)
	}
	return true
}

func TestConformance(t *testing.T) {
	path := guest(t)
	if goGuestRefused(t, path) {
		return
	}
	plugintest.Run(t, func(ctx context.Context) (plugin.Backend, error) {
		b, err := cshared.Open(ctx, path)
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() {
			_ = b.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := b.WaitClosed(ctx); err != nil {
				t.Error(err)
			}
		})
		return b, nil
	})
}

func TestOpenRejectsRelativePath(t *testing.T) {
	if _, err := cshared.Open(context.Background(), "guest.so"); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

func TestOpenHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cshared.Open(ctx, filepath.Join(t.TempDir(), "guest.so")); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestOpenReportsLoadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-library")
	if err := os.WriteFile(path, []byte("not a shared library"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := cshared.Open(context.Background(), path)
	if !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}

// TestImageStaysResident opens one library twice: the second open reuses the
// pinned image, and each handle closes on its own.
func TestImageStaysResident(t *testing.T) {
	path := guest(t)
	if goGuestRefused(t, path) {
		return
	}
	for range 2 {
		b, err := cshared.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Handshake(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = b.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		if err := b.WaitClosed(ctx); err != nil {
			t.Error(err)
		}
		cancel()
	}
}
