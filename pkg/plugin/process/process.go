// Package process starts a plugin as a child process and binds it to the
// JSON-line protocol over its stdin and stdout. It is a plugin.Backend for
// plugin.Open. It chooses no executable: the consumer supplies the whole
// command, including a managed runtime such as an interpreter plus script.
package process

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/jsonline"
)

// DefaultStderrBytes is the retained diagnostic output when none is set.
const DefaultStderrBytes = 4096

// Command is the consumer's launch decision.
type Command struct {
	// Path is the executable (a regular file, after following links).
	Path string
	// Args are the arguments after the executable name.
	Args []string
	// Dir is the working directory; empty inherits the host's.
	Dir string
	// Env adds "KEY=value" entries to the scrubbed base environment. The
	// host environment is never inherited; the base is PATH, plus SystemRoot
	// on Windows.
	Env []string
	// StderrBytes bounds retained stderr; zero uses DefaultStderrBytes.
	StderrBytes int
	// KillGrace bounds waiting for pipes after the process is killed.
	// Zero uses two seconds.
	KillGrace time.Duration
}

// Process is a started child and the JSON-line client bound to it. Canceling a
// call kills the process, and a killed or closed Process cannot be reused:
// later calls return plugin.ErrClosed.
type Process struct {
	*jsonline.Client
	conn *conn
}

// Start launches the command. ctx bounds only startup; use plugin.Open (or
// Handshake) to perform and check the hello exchange.
func Start(ctx context.Context, c Command) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Path == "" || c.StderrBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	info, err := os.Stat(c.Path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: launch executable must be a regular file", plugin.ErrInvalid)
	}
	if err := checkLaunchable(runtime.GOOS, c.Path); err != nil {
		return nil, err
	}
	env, err := environment(c.Env)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = env
	grace := c.KillGrace
	if grace == 0 {
		grace = 2 * time.Second
	}
	cmd.WaitDelay = grace
	limit := c.StderrBytes
	if limit == 0 {
		limit = DefaultStderrBytes
	}
	stderr := &bounded{limit: limit}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	cn := &conn{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}
	return &Process{Client: jsonline.NewClient(cn), conn: cn}, nil
}

// Stderr returns the retained (bounded, possibly truncated) diagnostic output.
// Treat it as untrusted text.
func (p *Process) Stderr() string { return p.conn.stderr.String() }

// checkLaunchable explains the Windows rule up front: the launch path must name
// the exact executable file, extension included. Go would otherwise report
// only "executable file not found" for a package file such as bin/provider.
func checkLaunchable(goos, path string) error {
	if goos != "windows" {
		return nil
	}
	extension := strings.ToLower(filepath.Ext(path))
	for _, allowed := range []string{".exe", ".com", ".bat", ".cmd"} {
		if extension == allowed {
			return nil
		}
	}
	return fmt.Errorf("%w: on Windows the launch path must name the exact executable file with an extension such as .exe; %q has none of .exe, .com, .bat or .cmd", plugin.ErrInvalid, filepath.Base(path))
}

func environment(extra []string) ([]string, error) {
	env := []string{"PATH=" + os.Getenv("PATH")}
	if runtime.GOOS == "windows" {
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	for _, e := range extra {
		k, _, ok := strings.Cut(e, "=")
		if !ok || k == "" || strings.ContainsRune(e, 0) {
			return nil, fmt.Errorf("%w: environment entries must be KEY=value", plugin.ErrInvalid)
		}
		env = append(env, e)
	}
	return env, nil
}

type conn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *bounded
	once   sync.Once
}

func (c *conn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *conn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

// Close kills the process, closes both pipes and reaps it. Idempotent.
func (c *conn) Close() error {
	c.once.Do(func() {
		_ = c.cmd.Process.Kill()
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		_ = c.cmd.Wait()
	})
	return nil
}

type bounded struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (b *bounded) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - b.buf.Len(); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		b.buf.Write(p[:room])
	}
	return len(p), nil
}

func (b *bounded) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var _ plugin.Backend = (*Process)(nil)

// Profile describes this backend for packagekit entrypoint selection: the
// "process" runtime speaking plugin.APIVersion over JSON lines, with the host
// owning the child process.
func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "process", Protocols: []string{plugin.APIVersion}, Cancellation: "kill", ProcessOwner: "host"}
}
