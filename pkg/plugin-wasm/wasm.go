// Package wasm runs WASI Preview 1 command guests using wazero. Guests speak
// ext.plugin/v1 JSON lines on stdin/stdout; Go authors use jsonline.ServeStdio.
// No host environment, filesystem or network is inherited.
package wasm

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/jsonline"
)

const MaxModuleBytes = 64 << 20
const DefaultMemoryPages = 4096 // 256 MiB of guest linear memory.

type Options struct {
	// MemoryLimitPages bounds guest linear memory (64 KiB/page). Zero uses 4096.
	// This is not a total host memory or compilation budget.
	MemoryLimitPages uint32
	// FS explicitly grants a filesystem, mounted at /. Nil grants none. The
	// host must provide a confined implementation: os.DirFS follows symlinks.
	FS   fs.FS
	Args []string
	Env  map[string]string
	// Stderr receives guest-controlled diagnostics; nil discards them. Writers
	// and filesystem operations supplied by the host must return promptly.
	Stderr io.Writer
	// MaxStderrBytes caps forwarded diagnostics for the guest lifetime. Zero
	// defaults to 64 KiB. Additional output is discarded without blocking.
	MaxStderrBytes int64
}

type Backend struct {
	*jsonline.Client
	done    chan struct{}
	exitErr error // published by closing done
}

// Open compiles verified module bytes and starts one persistent WASI command.
// Startup readiness is established by the Session handshake. Use inside
// plugin.Options.Connect; ctx bounds compilation, not the resulting session.
// Close or cancellation of a dispatched call terminates the entire module.
func Open(ctx context.Context, module []byte, opts Options) (*Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(module) == 0 || len(module) > MaxModuleBytes || opts.MemoryLimitPages > 65536 || opts.MaxStderrBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	if opts.MemoryLimitPages == 0 {
		opts.MemoryLimitPages = DefaultMemoryPages
	}
	if opts.MaxStderrBytes == 0 {
		opts.MaxStderrBytes = 64 << 10
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if len(opts.Args) > 256 || len(opts.Env) > 256 {
		return nil, plugin.ErrInvalid
	}
	for _, arg := range opts.Args {
		if arg == "" || len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return nil, plugin.ErrInvalid
		}
	}
	for key, value := range opts.Env {
		if key == "" || len(key) > 256 || strings.ContainsAny(key, "=\x00") || len(value) > 4096 || strings.ContainsRune(value, 0) {
			return nil, plugin.ErrInvalid
		}
	}
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(opts.MemoryLimitPages).WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		_ = r.Close(context.Background())
		return nil, err
	}
	compiled, err := r.CompileModule(ctx, module)
	if err != nil {
		_ = r.Close(context.Background())
		return nil, fmt.Errorf("compile WASI plugin: %w", err)
	}
	if _, ok := compiled.ExportedFunctions()["_start"]; !ok {
		_ = r.Close(context.Background())
		return nil, fmt.Errorf("%w: expected a WASI Preview 1 command exporting _start", plugin.ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		_ = r.Close(context.Background())
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	stdin, hostWrite := io.Pipe()
	hostRead, stdout := io.Pipe()
	conn := &connection{reader: hostRead, writer: hostWrite, stdin: stdin, stdout: stdout, cancel: cancel}
	config := wazero.NewModuleConfig().WithName("ctx-plugin").
		WithStdin(stdin).WithStdout(stdout).
		WithStderr(&limitedWriter{writer: opts.Stderr, remaining: opts.MaxStderrBytes}).
		WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader).
		WithNanosleep(func(ns int64) {
			timer := time.NewTimer(time.Duration(ns))
			defer timer.Stop()
			select {
			case <-runCtx.Done():
			case <-timer.C:
			}
		})
	if opts.FS != nil {
		config = config.WithFS(opts.FS)
	}
	if len(opts.Args) != 0 {
		config = config.WithArgs(append([]string(nil), opts.Args...)...)
	}
	keys := make([]string, 0, len(opts.Env))
	for key := range opts.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		config = config.WithEnv(key, opts.Env[key])
	}
	b := &Backend{Client: jsonline.NewClient(conn), done: make(chan struct{})}
	go func() {
		_, err := r.InstantiateModule(runCtx, compiled, config)
		var exit *sys.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 0 {
			err = nil
		}
		// Deliver a trap/startup failure to any pending handshake or call.
		_ = conn.shutdown(err)
		// Closing the runtime concurrently with WASI calls races its filesystem
		// state. Context cancellation stops execution without freeing resources;
		// only the execution owner releases them after InstantiateModule returns.
		b.exitErr = errors.Join(err, r.Close(context.Background()))
		close(b.done)
	}()
	return b, nil
}

// Wait observes guest exit, including startup failures or traps. Close first
// when stopping a long-running guest. Resources are released after execution
// returns. Host-supplied blocking I/O and debug stack decoding may delay exit.
func (b *Backend) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return b.exitErr
	}
}

func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "wasm/wasi-preview1", Protocols: []string{plugin.APIVersion}, Cancellation: "module", ProcessOwner: "wazero"}
}

type connection struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	stdin  *io.PipeReader
	stdout *io.PipeWriter
	cancel context.CancelFunc
	once   sync.Once
}

func (c *connection) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *connection) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *connection) Close() error                { return c.shutdown(plugin.ErrClosed) }
func (c *connection) shutdown(cause error) error {
	c.once.Do(func() {
		c.cancel()
		// Closing the guest ends unblocks both host ends while preserving the
		// exit error. Closing the host ends too would mask it as ErrClosedPipe.
		_ = c.stdin.CloseWithError(cause)
		_ = c.stdout.CloseWithError(cause)
	})
	return nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if int64(len(p)) > w.remaining {
		p = p[:int(w.remaining)]
	}
	if len(p) != 0 {
		written, err := w.writer.Write(p)
		w.remaining -= int64(written)
		if err != nil {
			return written, err
		}
		if written != len(p) {
			return written, io.ErrShortWrite
		}
	}
	return n, nil
}
