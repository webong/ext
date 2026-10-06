// Package crosslang tests real foreign artifacts built by scripts/plugin-crosslang.sh.
// Ordinary Go-only runs skip these tests; CI requires every artifact explicitly.
package crosslang_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin-cshared"
	"github.com/webong/ctx/pkg/plugin-wasm"
	"github.com/webong/ctx/pkg/plugin/jsonline"
	"github.com/webong/ctx/pkg/plugin/nativego"
	"github.com/webong/ctx/pkg/plugin/plugintest"
)

func TestForeignMalformedFrames(t *testing.T) {
	data, err := os.ReadFile("../../plugin/testdata/v1/invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	var invalid []string
	if err := json.Unmarshal(data, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid = append(invalid, string([]byte{0xff}), strings.Repeat("[", 66)+"0"+strings.Repeat("]", 66))
	for _, language := range []string{"RUST", "ZIG"} {
		t.Run(language, func(t *testing.T) {
			path := artifact(t, language+"_GUEST")
			for _, raw := range invalid {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				cmd := exec.CommandContext(ctx, path)
				cmd.Stdin = bytes.NewBufferString(raw + "\n")
				out, err := cmd.Output()
				cancel()
				if err == nil || len(out) != 0 {
					t.Fatalf("malformed input accepted: %q, output %q, error %v", raw, out, err)
				}
			}
		})
	}
}

func artifact(t *testing.T, name string) string {
	t.Helper()
	p := os.Getenv("CTX_" + name)
	if p == "" {
		if os.Getenv("CTX_CROSSLANG_REQUIRED") == "1" {
			t.Fatalf("CTX_%s is required", name)
		}
		t.Skip("set CTX_" + name + " or run scripts/plugin-crosslang.sh")
	}
	p, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}

type processConn struct {
	io.Reader
	io.Writer
	read  io.Closer
	write io.Closer
	cmd   *exec.Cmd
	once  sync.Once
}

func (p *processConn) Close() error {
	p.once.Do(func() { _ = p.read.Close(); _ = p.write.Close(); _ = p.cmd.Process.Kill(); _ = p.cmd.Wait() })
	return nil
}
func process(path string) (plugin.Backend, error) {
	cmd := exec.Command(path)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	return jsonline.NewClient(&processConn{Reader: out, Writer: in, read: out, write: in, cmd: cmd}), nil
}
func TestForeignGuests(t *testing.T) {
	for _, language := range []string{"RUST", "ZIG"} {
		t.Run(language, func(t *testing.T) {
			t.Run("jsonline", func(t *testing.T) {
				p := artifact(t, language+"_GUEST")
				runGuestConformance(t, func(context.Context) (plugin.Backend, error) { return process(p) })
			})
			t.Run("cshared", func(t *testing.T) {
				p := artifact(t, language+"_SHARED")
				runGuestConformance(t, func(ctx context.Context) (plugin.Backend, error) {
					b, err := cshared.Open(ctx, p)
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
			})
			t.Run("wasi", func(t *testing.T) {
				data, err := os.ReadFile(artifact(t, language+"_WASM"))
				if err != nil {
					t.Fatal(err)
				}
				runGuestConformance(t, func(ctx context.Context) (plugin.Backend, error) {
					b, err := wasm.Open(ctx, data, wasm.Options{})
					if err != nil {
						return nil, err
					}
					t.Cleanup(func() {
						_ = b.Close()
						ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
						defer cancel()
						err := b.Wait(ctx)
						if ctx.Err() != nil {
							var stacks bytes.Buffer
							_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
							t.Errorf("WASI cleanup: %v\n%s", err, stacks.String())
						}
					})
					return b, nil
				})
			})
		})
	}
}
func TestForeignHosts(t *testing.T) {
	for _, language := range []string{"RUST", "ZIG"} {
		t.Run(language, func(t *testing.T) {
			host := artifact(t, language+"_HOST")
			shared := artifact(t, "GO_SHARED")
			args := []string{shared}
			if language == "RUST" {
				args = []string{"cshared", shared}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, host, args...).CombinedOutput(); err != nil {
				t.Fatalf("foreign host: %v\n%s", err, out)
			}
			if language == "RUST" {
				guest := artifact(t, "GO_GUEST")
				if out, err := exec.CommandContext(ctx, host, "process", guest).CombinedOutput(); err != nil {
					t.Fatalf("Rust process host: %v\n%s", err, out)
				}
			} else {
				cmd := exec.CommandContext(ctx, host, "--stdio")
				in, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				out, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err = cmd.Start(); err != nil {
					t.Fatal(err)
				}
				g, err := plugintest.Guest()
				if err != nil {
					t.Fatal(err)
				}
				conn := &duplex{Reader: out, Writer: in, read: out, write: in}
				if err := jsonline.ServeGuest(ctx, conn, g); err != nil {
					t.Error(err)
				}
				if err := cmd.Wait(); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

type duplex struct {
	io.Reader
	io.Writer
	read  io.Closer
	write io.Closer
}

func (d *duplex) Close() error { return errors.Join(d.read.Close(), d.write.Close()) }

func TestNativeGoGuest(t *testing.T) {
	path := artifact(t, "GO_NATIVE")
	runGuestConformance(t, func(ctx context.Context) (plugin.Backend, error) { return nativego.Open(ctx, path) })
}
