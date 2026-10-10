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
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/process"
	"github.com/webong/ext/pkg/plugin/store"
)

// DefaultTimeout bounds handshake and calls without an earlier caller deadline.
const DefaultTimeout = 10 * time.Minute

// DrainTimeout is how long Close waits for admitted calls before aborting.
const DrainTimeout = 5 * time.Second

// Options supplies the admission policy and launch choice.
type Options struct {
	// Launch overrides how the package starts, for example to run source files
	// under a runtime the consumer manages (Path is that runtime, Args begin
	// with the script). Dir defaults to the package directory. When nil, the
	// package's first entrypoint artifact is started with no arguments.
	Launch *process.Command
	// Authorize runs before every call. When nil, any operation the reviewed
	// descriptor declares is allowed.
	Authorize func(context.Context, plugin.Request) error
	// Timeout bounds the handshake and calls without an earlier caller
	// deadline. Zero uses DefaultTimeout.
	Timeout time.Duration
}

// Process is one plugin process behind a plugin session.
type Process struct {
	session *plugin.Session
	proc    *process.Process
}

// Open re-verifies the installed package, starts it and completes the
// handshake. Verification binds the launch to the installed content; it is not
// publisher trust, which stays with the consumer.
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
	})
	if err != nil {
		return nil, err
	}
	result.session = session
	return result, nil
}

// Descriptor is a copy of the session's reviewed descriptor.
func (p *Process) Descriptor() plugin.Descriptor { return p.session.Descriptor() }

// Call invokes "<contract>.<operation>" (see plugin.Descriptor.ResolveMethod).
// params is JSON-encoded, with nil meaning JSON null. A non-nil result receives
// the decoded payload; a null payload leaves it untouched.
func (p *Process) Call(ctx context.Context, method string, params any, result any) error {
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
	raw, err := p.session.Call(ctx, ref, operation, payload)
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
