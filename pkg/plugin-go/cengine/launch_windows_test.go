//go:build ext_cengine && cgo && windows

package goengine

import (
	"context"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Batch files run through cmd.exe, whose argument parsing makes launching them
// an injection hazard. The C engine refuses them by design; the Go process
// package's looser rule is a known difference, listed in the parity notes.
func TestBatchFilesAreNotLaunched(t *testing.T) {
	for _, name := range []string{"run.cmd", "run.bat", "RUN.CMD", "noextension"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			h, err := New(path, plugintest.Descriptor(), allow, allow)
			if err != nil {
				if errors.Is(err, plugin.ErrInvalid) {
					return
				}
				t.Fatal(err)
			}
			defer h.Destroy()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := h.Start(ctx); !errors.Is(err, plugin.ErrInvalid) {
				t.Fatalf("launched %s: %v", name, err)
			}
		})
	}
}
