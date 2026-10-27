package wasm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

var spinModule = []byte{
	0, 97, 115, 109, 1, 0, 0, 0,
	1, 4, 1, 96, 0, 0,
	3, 2, 1, 0,
	7, 10, 1, 6, '_', 's', 't', 'a', 'r', 't', 0, 0,
	10, 9, 1, 7, 0, 3, 64, 12, 0, 11, 11,
}

func TestBackendMapsWebErrorsAndCancels(t *testing.T) {
	for _, opts := range []Options{{MemoryLimitPages: 65537}, {MaxStderrBytes: -1}} {
		if _, err := Open(context.Background(), spinModule, opts); !errors.Is(err, plugin.ErrInvalid) {
			t.Fatalf("options %+v: %v", opts, err)
		}
	}
	if _, err := Open(context.Background(), spinModule[:8], Options{}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("unsupported module: %v", err)
	}

	backend, err := Open(context.Background(), spinModule, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, err = backend.Handshake(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake of a nonresponsive guest: %v", err)
	}
	var closers sync.WaitGroup
	for range 4 {
		closers.Add(1)
		go func() {
			defer closers.Done()
			_ = backend.Close()
		}()
	}
	closers.Wait()
	wait, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := backend.Wait(wait); errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancelled command did not finish cleanup")
	}
}

func exportingModule(name string) []byte {
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	module = append(module, 1, 4, 1, 0x60, 0, 0)
	module = append(module, 3, 2, 1, 0)
	section := append([]byte{1, byte(len(name))}, name...)
	section = append(section, 0, 0)
	module = append(module, 7, byte(len(section)))
	module = append(module, section...)
	return append(module, 10, 4, 1, 2, 0, 0x0b)
}

func TestReactorMapsWebErrors(t *testing.T) {
	if _, err := OpenReactor(context.Background(), nil, ReactorOptions{}); !errors.Is(err, plugin.ErrInvalid) {
		t.Fatalf("empty reactor: %v", err)
	}
	if _, err := OpenReactor(context.Background(), exportingModule("_start"), ReactorOptions{}); !errors.Is(err, plugin.ErrUnsupported) {
		t.Fatalf("command reactor: %v", err)
	}
}
