// Package crosslang tests real foreign artifacts built by scripts/plugin-crosslang.sh.
// Ordinary Go-only runs skip these tests; CI requires every artifact explicitly.
package crosslang_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/webong/ext/pkg/plugin-go/cengine/cshared"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/nativego"
	"github.com/webong/ext/pkg/plugin-go/contract/plugintest"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	"github.com/webong/ext/pkg/plugin-go/wasm"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForeignMalformedFrames(t *testing.T) {
	for _, language := range []string{"RUST", "ZIG"} {
		t.Run(language, func(t *testing.T) {
			rejectMalformedFrames(t, artifact(t, language+"_GUEST"))
		})
	}
}

// rejectMalformedFrames checks that a guest process fails, writing nothing, on
// every malformed first frame.
func rejectMalformedFrames(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile("../../../lib/plugin/testdata/v1/invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	var invalid []string
	if err := json.Unmarshal(data, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid = append(invalid, string([]byte{0xff}), strings.Repeat("[", 66)+"0"+strings.Repeat("]", 66))
	for _, raw := range invalid {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, path)
		cmd.Stdin = bytes.NewBufferString(raw + "\n")
		out, err := cmd.Output()
		cancel()
		if err == nil || len(out) != 0 {
			t.Fatalf("malformed input accepted: %q, output %q, error %v", raw, out, err)
		}
	}
}

func artifact(t *testing.T, name string) string {
	t.Helper()
	p := os.Getenv("EXT_" + name)
	if p == "" {
		if os.Getenv("EXT_CROSSLANG_REQUIRED") == "1" {
			t.Fatalf("EXT_%s is required", name)
		}
		t.Skip("set EXT_" + name + " or run scripts/plugin-crosslang.sh")
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

// sharedBackend opens a dynamic library through the cshared ABI and closes it
// with the test.
func sharedBackend(t *testing.T, path string) func(context.Context) (plugin.Backend, error) {
	return func(ctx context.Context) (plugin.Backend, error) {
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
	}
}

// TestRustReactorGuest runs the conformance suite against the Rust SDK's shared
// guest compiled to a WebAssembly reactor, hosted by wasm.OpenReactor: the C ABI
// plus ext_plugin_alloc/ext_plugin_free (docs/plugin-reactor-abi.md). The artifact
// is optional unless scripts/plugin-crosslang.sh sets EXT_REACTOR_REQUIRED=1.
func TestRustReactorGuest(t *testing.T) {
	if os.Getenv("EXT_RUST_REACTOR") == "" && os.Getenv("EXT_REACTOR_REQUIRED") != "1" {
		t.Skip("set EXT_RUST_REACTOR to the wasm32-wasip1 build of the Rust shared example")
	}
	module, err := os.ReadFile(artifact(t, "RUST_REACTOR"))
	if err != nil {
		t.Fatal(err)
	}
	runGuestConformance(t, func(ctx context.Context) (plugin.Backend, error) {
		b, err := wasm.OpenReactor(ctx, module, wasm.ReactorOptions{})
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

// javaGuest returns the launcher scripts/ext-android.sh builds, or skips. Java is
// optional so ordinary runs need no JDK; the script sets EXT_JAVA_REQUIRED=1.
func javaGuest(t *testing.T) string {
	t.Helper()
	if os.Getenv("EXT_JAVA_GUEST") == "" && os.Getenv("EXT_JAVA_REQUIRED") != "1" {
		t.Skip("set EXT_JAVA_GUEST to the launcher built by scripts/ext-android.sh")
	}
	return artifact(t, "JAVA_GUEST")
}

// TestJavaJSONLineGuest runs the conformance suite against the Java guest
// process from pkg/ext-android, over the JSON-line transport.
func TestJavaJSONLineGuest(t *testing.T) {
	p := javaGuest(t)
	runGuestConformance(t, func(context.Context) (plugin.Backend, error) { return process(p) })
}

func TestJavaMalformedFrames(t *testing.T) {
	rejectMalformedFrames(t, javaGuest(t))
}

// TestSwiftSharedGuest runs the conformance suite against the fixture that
// pkg/ext-ios builds, through the same cshared ABI the other languages use.
//
// Swift builds only on Apple platforms, so the artifact is optional elsewhere:
// scripts/plugin-crosslang.sh sets EXT_SWIFT_REQUIRED=1 on macOS.
func TestSwiftSharedGuest(t *testing.T) {
	if os.Getenv("EXT_SWIFT_SHARED") == "" && os.Getenv("EXT_SWIFT_REQUIRED") != "1" {
		t.Skip("set EXT_SWIFT_SHARED to the library built by pkg/ext-ios")
	}
	runGuestConformance(t, sharedBackend(t, artifact(t, "SWIFT_SHARED")))
}

func TestForeignGuests(t *testing.T) {
	for _, language := range []string{"RUST", "ZIG"} {
		t.Run(language, func(t *testing.T) {
			t.Run("jsonline", func(t *testing.T) {
				p := artifact(t, language+"_GUEST")
				runGuestConformance(t, func(context.Context) (plugin.Backend, error) { return process(p) })
			})
			t.Run("cshared", func(t *testing.T) {
				runGuestConformance(t, sharedBackend(t, artifact(t, language+"_SHARED")))
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

// TestFramesGuests runs the frames suite (progress events and reverse calls, see
// docs/plugin-guest-frames.md) on every guest that implements it. The Go reference
// guest is always built by scripts/plugin-crosslang.sh; a language joins by setting
// EXT_<NAME>_FRAMES_GUEST to a process that serves the descriptor in
// lib/plugin/testdata/v1/frames-descriptor.json (or EXT_<NAME>_FRAMES_WASM to a WASI
// command that does). A guest is concurrent when it can serve a request while it waits
// for the host's answer (a thread or an event loop); one that cannot answers the
// operations that need it with the error "unsupported". EXT_FRAMES_REQUIRED lists
// (separated by spaces or commas) the guests that must be present, as GO, RUST,
// RUST_INLINE or RUST_WASI.
func TestFramesGuests(t *testing.T) {
	required := func(name string) bool {
		for _, r := range strings.FieldsFunc(os.Getenv("EXT_FRAMES_REQUIRED"), func(c rune) bool { return c == ' ' || c == ',' }) {
			if r == name {
				return true
			}
		}
		return false
	}
	for _, g := range []struct {
		name       string
		concurrent bool
	}{
		{"GO", true},
		{"RUST", true},
		{"RUST_INLINE", false},
		{"TS", true},
		{"JAVA", true},
		{"SWIFT", true},
		{"PYTHON", false},
		{"ZIG", false},
	} {
		t.Run(g.name, func(t *testing.T) {
			variable := g.name + "_FRAMES_GUEST"
			if os.Getenv("EXT_"+variable) == "" && !required(g.name) {
				t.Skip("set EXT_" + variable + " to a guest process that serves the frames descriptor")
			}
			path := artifact(t, variable)
			runFrames(t, func(context.Context) (plugin.Backend, error) { return process(path) }, path, plugintest.FramesOptions{Concurrent: g.concurrent})
		})
	}
	// A WebAssembly command speaks JSON lines through the same client as a process,
	// without threads.
	for _, g := range []string{"RUST", "ZIG"} {
		t.Run(g+"_WASI", func(t *testing.T) {
			variable := g + "_FRAMES_WASM"
			if os.Getenv("EXT_"+variable) == "" && !required(g+"_WASI") {
				t.Skip("set EXT_" + variable + " to a WASI command that serves the frames descriptor")
			}
			data, err := os.ReadFile(artifact(t, variable))
			if err != nil {
				t.Fatal(err)
			}
			runFrames(t, func(ctx context.Context) (plugin.Backend, error) {
				b, err := wasm.Open(ctx, data, wasm.Options{})
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() {
					_ = b.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
					defer cancel()
					if err := b.Wait(ctx); ctx.Err() != nil {
						t.Errorf("WASI cleanup: %v", err)
					}
				})
				return b, nil
			}, "", plugintest.FramesOptions{Concurrent: false})
		})
	}
}
