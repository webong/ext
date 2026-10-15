// Package host opens an installed package as a running plugin and calls it by
// dotted method name. It composes the store (verified content), the C engine's
// process runtime (launch, stderr capture, drain) and its session (handshake,
// admission); it chooses no install location and grants no authority beyond the
// descriptor the caller reviewed.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	goengine "github.com/webong/ext/pkg/plugin-go"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/store"
	"path/filepath"
	"time"
)

// DefaultTimeout bounds handshake and calls without an earlier caller deadline.
const DefaultTimeout = 10 * time.Minute

// DrainTimeout is how long Close waits for admitted calls before aborting.
const DrainTimeout = 5 * time.Second

// Launch is the consumer's launch decision.
type Launch struct {
	// Path is the executable, a regular file.
	Path string
	// Args are the arguments after the executable name.
	Args []string
	// Dir is the working directory; empty means the package directory.
	Dir string
	// Env adds "KEY=value" entries to the scrubbed base environment of PATH.
	Env []string
	// StderrBytes bounds retained stderr; zero retains 4096 bytes.
	StderrBytes int
}

// Options supplies the admission policy and launch choice.
type Options struct {
	// Launch overrides how the package starts, for example to run source files
	// under a runtime the consumer manages (Path is that runtime, Args begin
	// with the script). When nil, the package's first entrypoint artifact is
	// started with no arguments.
	Launch *Launch
	// Authorize runs before every call. When nil, any operation the reviewed
	// descriptor declares is allowed.
	Authorize func(context.Context, plugin.Request) error
	// Timeout bounds the handshake and calls without an earlier caller
	// deadline. Zero uses DefaultTimeout.
	Timeout time.Duration
}

// Process is one plugin process behind a C engine session.
type Process struct {
	session *goengine.Session
}

// Open re-verifies the installed package, starts it and completes the
// handshake. Verification binds the launch to the installed content; it is not
// publisher trust, which stays with the consumer.
func Open(ctx context.Context, installed store.Installed, options Options) (*Process, error) {
	var launch Launch
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
	path, err := filepath.Abs(launch.Path)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(launch.Dir)
	if err != nil {
		return nil, err
	}
	authorize := options.Authorize
	if authorize == nil {
		authorize = func(context.Context, plugin.Request) error { return nil }
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	session, err := goengine.OpenProcess(ctx, installed.Manifest.Descriptor, goengine.LaunchOptions{
		ProcessOptions: goengine.ProcessOptions{Executable: path, Arguments: launch.Args, Environment: launch.Env},
		Directory:      dir,
		StderrBytes:    launch.StderrBytes,
	}, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return installed.Verify() },
		Authorize: authorize,
		Timeout:   timeout,
	})
	if err != nil {
		return nil, err
	}
	return &Process{session: session}, nil
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
func (p *Process) Stderr() string { return p.session.Stderr() }

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
