// Package wasm provides protocol-neutral WebAssembly inspection and execution
// using wazero. Callers explicitly grant every host resource exposed to a
// module; no environment, filesystem, network, or standard I/O is inherited.
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
)

const MaxModuleBytes = 64 << 20
const DefaultMemoryPages = 4096 // 256 MiB of guest linear memory.

// Options configures a persistent WASI Preview 1 command connected to the host
// through stdin and stdout.
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

// Command is one persistent WASI command. Read and Write exchange raw bytes
// with the command's stdout and stdin; this package assigns them no protocol.
type Command struct {
	conn    *connection
	done    chan struct{}
	exitErr error // published by closing done
}

// Open compiles module and starts one persistent WASI Preview 1 command. The
// returned stream is protocol-neutral. ctx bounds compilation, not the running
// command; Close terminates it.
func Open(ctx context.Context, module []byte, opts Options) (*Command, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(module) == 0 || len(module) > MaxModuleBytes || opts.MemoryLimitPages > 65536 || opts.MaxStderrBytes < 0 {
		return nil, ErrInvalid
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
		return nil, ErrInvalid
	}
	for _, arg := range opts.Args {
		if arg == "" || len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return nil, ErrInvalid
		}
	}
	for key, value := range opts.Env {
		if key == "" || len(key) > 256 || strings.ContainsAny(key, "=\x00") || len(value) > 4096 || strings.ContainsRune(value, 0) {
			return nil, ErrInvalid
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
		return nil, fmt.Errorf("compile WASI command: %w", err)
	}
	if _, ok := compiled.ExportedFunctions()["_start"]; !ok {
		_ = r.Close(context.Background())
		return nil, fmt.Errorf("%w: expected a WASI Preview 1 command exporting _start", ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		_ = r.Close(context.Background())
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	stdin, hostWrite := io.Pipe()
	hostRead, stdout := io.Pipe()
	conn := &connection{reader: hostRead, writer: hostWrite, stdin: stdin, stdout: stdout, cancel: cancel}
	config := wazero.NewModuleConfig().WithName("wasm-command").
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
	command := &Command{conn: conn, done: make(chan struct{})}
	go func() {
		_, err := r.InstantiateModule(runCtx, compiled, config)
		var exit *sys.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 0 {
			err = nil
		}
		// Deliver a trap/startup failure to pending reads and writes.
		_ = conn.shutdown(err)
		// The execution owner releases the runtime after InstantiateModule
		// returns so filesystem state is never closed concurrently with a call.
		command.exitErr = errors.Join(err, r.Close(context.Background()))
		close(command.done)
	}()
	return command, nil
}

func (c *Command) Read(p []byte) (int, error)  { return c.conn.Read(p) }
func (c *Command) Write(p []byte) (int, error) { return c.conn.Write(p) }
func (c *Command) Close() error                { return c.conn.Close() }

// Wait observes command exit, including startup failures or traps. Close first
// when stopping a long-running command.
func (c *Command) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.exitErr
	}
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
func (c *connection) Close() error                { return c.shutdown(ErrClosed) }
func (c *connection) shutdown(cause error) error {
	c.once.Do(func() {
		c.cancel()
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
