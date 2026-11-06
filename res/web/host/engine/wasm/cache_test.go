package wasm

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
)

// directoryCache returns a cache that also writes every module it compiles to a directory, and
// a count of the files there. That is how a test sees that a session compiled through the cache
// it was given: wazero offers no other hook.
func directoryCache(t *testing.T) (*CompilationCache, func() int) {
	t.Helper()
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("wazero compiles to machine code, and so caches it, on amd64 and arm64 only")
	}
	dir := t.TempDir()
	inner, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	cache := &CompilationCache{cache: inner}
	t.Cleanup(func() { _ = cache.Close(context.Background()) })
	return cache, func() int {
		files := 0
		err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
			if err == nil && entry.Type().IsRegular() {
				files++
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
}

func TestEveryExecutionPathCompilesThroughTheCacheItIsGiven(t *testing.T) {
	ctx := context.Background()
	paths := []struct {
		name string
		open func(*CompilationCache) error
	}{
		{"Open", func(cache *CompilationCache) error {
			b, err := Open(ctx, spinModule, Options{CompilationCache: cache})
			if err != nil {
				return err
			}
			_ = b.Close()
			wait, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			// Closing a running command ends it with an error; only a hang is a failure.
			if err := b.Wait(wait); errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return nil
		}},
		{"OpenReactor", func(cache *CompilationCache) error {
			// Not a reactor, so it is refused, but only after it was compiled.
			_, err := OpenReactor(ctx, exportingModule("other"), testReactorABI, ReactorOptions{CompilationCache: cache})
			if err == nil {
				t.Error("accepted a module that is not a reactor")
			}
			return nil
		}},
		{"RunCommand", func(cache *CompilationCache) error {
			code, err := RunCommand(ctx, exportingModule("_start"), CommandOptions{CompilationCache: cache})
			if err != nil || code != 0 {
				t.Errorf("RunCommand = %d, %v", code, err)
			}
			return nil
		}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			cache, files := directoryCache(t)
			if n := files(); n != 0 {
				t.Fatalf("a new cache holds %d files", n)
			}
			if err := path.open(cache); err != nil {
				t.Fatal(err)
			}
			if files() == 0 {
				t.Error("the module was compiled, but not through the cache in the options")
			}
		})
	}
}

func TestACacheServesLaterOpensOfTheSameModule(t *testing.T) {
	cache := NewCompilationCache()
	defer cache.Close(context.Background())
	for i := 0; i < 3; i++ {
		b, err := Open(context.Background(), spinModule, Options{CompilationCache: cache})
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		_ = b.Close()
		wait, stop := context.WithTimeout(context.Background(), 2*time.Second)
		err = b.Wait(wait)
		stop()
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("open %d did not shut down", i)
		}
	}
}

func TestClosingANilCacheIsNotAnError(t *testing.T) {
	var cache *CompilationCache
	if err := cache.Close(context.Background()); err != nil {
		t.Errorf("closing a nil cache: %v", err)
	}
}
