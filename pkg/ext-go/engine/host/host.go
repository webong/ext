// Package host opens an installed package as a running plugin and calls it by
// dotted method name. It composes store (verified content), process (launch)
// and the plugin session (handshake, admission); it chooses no install
// location and grants no authority beyond the descriptor the caller reviewed.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

// DefaultTimeout bounds handshake and calls without an earlier caller deadline.
const DefaultTimeout = 10 * time.Minute

// DrainTimeout is how long Close waits for admitted calls before aborting.
const DrainTimeout = 5 * time.Second

// ReverseOptions configures the services a host offers to its plugin; it is
// plugin.ReverseOptions, so a host need not import the root package for it.
type ReverseOptions = plugin.ReverseOptions

// Options supplies the admission policy and launch choice.
type Options struct {
	// Launch overrides how the package starts, for example to run source files
	// under a runtime the consumer manages (Path is that runtime, Args begin
	// with the script). Dir defaults to the package directory, not the caller's
	// working directory: a plugin that runs commands for the user needs Dir set
	// explicitly. When nil, the
	// package's first entrypoint artifact is started with no arguments.
	Launch *process.Command
	// Authorize runs before every call. When nil, any operation the reviewed
	// descriptor declares is allowed.
	Authorize func(context.Context, plugin.Request) error
	// Timeout bounds the handshake and calls without an earlier caller
	// deadline. Zero uses DefaultTimeout.
	Timeout time.Duration
	// Env adds KEY=value entries to the child's environment, which is otherwise
	// scrubbed (PATH only, plus SystemRoot on Windows). It applies to the default
	// launch and is added to Launch.Env when Launch is set. A name may appear only
	// once across both.
	Env []string
	// Reverse offers services to the plugin: calls from the plugin to this
	// host, limited to the operations its descriptor declares in Reverse. When
	// nil, every reverse call is denied.
	Reverse *ReverseOptions
}

// Process is one plugin process behind a plugin session.
type Process struct {
	session *plugin.Session
	proc    *process.Process
}

// Open re-verifies the installed package, starts it and completes the
// handshake. Verification binds the launch to the installed content; it is not
// publisher trust, which stays with the consumer.
//
// ctx bounds the start and the handshake only. It does not bound the process:
// cancelling it after Open returns does not stop the plugin or its calls. Close
// ends the process, and each Call takes its own context.
func Open(ctx context.Context, installed store.Installed, options Options) (*Process, error) {
	var launch process.Command
	if options.Launch != nil {
		launch = *options.Launch
		if launch.Path == "" {
			return nil, fmt.Errorf("%w: launch path is required", plugin.ErrInvalid)
		}
	} else {
		path, err := installed.Executable()
		if err != nil {
			return nil, err
		}
		launch.Path = path
	}
	if launch.Dir == "" {
		launch.Dir = installed.Directory
	}
	env, err := withEnv(launch.Env, options.Env)
	if err != nil {
		return nil, err
	}
	launch.Env = env
	authorize := options.Authorize
	if authorize == nil {
		authorize = func(context.Context, plugin.Request) error { return nil }
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	result := &Process{}
	session, err := plugin.Open(ctx, installed.Manifest.Descriptor, plugin.Options{
		Verify: func(context.Context, plugin.Descriptor) error { return installed.Verify() },
		Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
			proc, err := process.Start(ctx, launch)
			if err != nil {
				return nil, err
			}
			result.proc = proc
			return proc, nil
		},
		Authorize: authorize,
		Timeout:   timeout,
		Reverse:   options.Reverse,
	})
	if err != nil {
		return nil, err
	}
	result.session = session
	return result, nil
}

// Done is closed once the plugin session has ended, whether by Close, an abort or
// a failed call. Anything whose lifetime is tied to the plugin, such as an
// endpoint grant (endpoint.Admit takes it), ends with it.
func (p *Process) Done() <-chan struct{} { return p.session.Done() }

// Descriptor is a copy of the session's reviewed descriptor.
func (p *Process) Descriptor() plugin.Descriptor { return p.session.Descriptor() }

// Call invokes "<contract>.<operation>" (see plugin.Descriptor.ResolveMethod).
// params is JSON-encoded, with nil meaning JSON null. A non-nil result receives
// the decoded payload; a null payload leaves it untouched.
func (p *Process) Call(ctx context.Context, method string, params any, result any) error {
	return p.CallProgress(ctx, method, params, result, nil)
}

// CallProgress is Call for an operation that declares progress: onProgress
// receives its progress and log events while the call runs. It must not block.
// Events beyond the rate limit are dropped and counted in the next event's
// Dropped field. The result is unchanged.
func (p *Process) CallProgress(ctx context.Context, method string, params any, result any, onProgress func(plugin.Progress), options ...plugin.CallOptions) error {
	ref, operation, err := p.session.Descriptor().ResolveMethod(method)
	if err != nil {
		return err
	}
	payload := json.RawMessage("null")
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return err
		}
		payload = encoded
	}
	raw, err := p.session.CallProgress(ctx, ref, operation, payload, onProgress, options...)
	if err != nil {
		return err
	}
	if result == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, result)
}

// Stderr returns bounded, untrusted diagnostic output from the child.
func (p *Process) Stderr() string {
	if p.proc == nil {
		return ""
	}
	return p.proc.Stderr()
}

// Close drains admitted calls for DrainTimeout, then aborts the child.
func (p *Process) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer cancel()
	err := p.session.Close(ctx)
	if err != nil && !errors.Is(err, plugin.ErrClosed) {
		_ = p.session.Abort()
	}
	return err
}

// withEnv returns the launch environment with extra entries added. Both lists
// are KEY=value entries; a name may appear only once across them, so what the
// child sees never depends on an order or on a platform's duplicate rule.
func withEnv(launch, extra []string) ([]string, error) {
	if len(extra) == 0 {
		return launch, nil
	}
	merged := append(append([]string(nil), launch...), extra...)
	seen := map[string]bool{}
	for _, entry := range merged {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsRune(entry, 0) || seen[key] {
			return nil, fmt.Errorf("%w: environment entries must be KEY=value with distinct names", plugin.ErrInvalid)
		}
		seen[key] = true
	}
	return merged, nil
}
