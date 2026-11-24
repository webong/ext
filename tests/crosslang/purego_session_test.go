package crosslang_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/bind"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/endpoint"
	"github.com/webong/ext/tests/crosslang/internal/pluginbind"
)

// The C engine through purego, reached through pluginbind: the binding ext lib bind generates from
// lib/plugin/interface.json, committed here and held to the description by
// TestPluginBindIsCurrent. pureGoSession gives it the engine's session API (Go policies, observer
// and reverse handler; contexts) so the differential checks the other engines pass hold it too. It
// is test code: ext-go carries no binding of lib/plugin.

// TestPluginBindIsCurrent regenerates the binding and requires it to equal the committed one; set
// EXT_UPDATE=1 to rewrite it.
func TestPluginBindIsCurrent(t *testing.T) {
	const description, generated = "../../lib/plugin/interface.json", "internal/pluginbind/pluginbind.go"
	data, err := os.ReadFile(description)
	if err != nil {
		t.Fatal(err)
	}
	var d library.Description
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	src, err := bind.Generate(d, bind.Options{Package: "pluginbind", Source: description})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("EXT_UPDATE") == "1" {
		if err := os.WriteFile(generated, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	have, err := os.ReadFile(generated)
	if err != nil || string(have) != string(src) {
		t.Fatalf("%s is not the binding of %s: run EXT_UPDATE=1 go test -run TestPluginBindIsCurrent", generated, description)
	}
}

// codeErrors are the engine errors lib/plugin's code names stand for.
var codeErrors = map[string]error{
	"invalid": engine.ErrInvalid, "denied": engine.ErrDenied, "mismatch": engine.ErrMismatch,
	"unsupported": engine.ErrUnsupported, "closed": engine.ErrClosed, "timeout": context.DeadlineExceeded,
	"draining": engine.ErrDraining, "not_found": engine.ErrNotFound, "ambiguous": engine.ErrAmbiguous,
	"canceled": context.Canceled, "protocol": engine.ErrProtocol, "expired": endpoint.ErrExpired,
}

func codeName(err error) string {
	var code *library.CodeError
	if errors.As(err, &code) {
		return code.Name
	}
	return ""
}

func engineError(err error) error {
	if sentinel := codeErrors[codeName(err)]; sentinel != nil {
		return fmt.Errorf("%w: %w", err, sentinel)
	}
	return err
}

func timeoutMS(ctx context.Context, timeout time.Duration) int64 {
	if timeout <= 0 {
		timeout = engine.DefaultTimeout
	}
	if until, ok := ctx.Deadline(); ok && time.Until(until) < timeout {
		timeout = time.Until(until)
	}
	return min(max(timeout.Milliseconds(), 1), math.MaxUint32)
}

// pureGoLaunch is a child process the engine starts.
type pureGoLaunch struct {
	Executable  string
	Arguments   []string
	Environment []string
}

type pureGoSession struct {
	host       *pluginbind.Host
	descriptor engine.Descriptor
	options    engine.Options
	base       context.Context
	mu         sync.Mutex
	verifyErr  error
	once       sync.Once
	closeErr   error
	done       chan struct{}
}

func openPureGo(ctx context.Context, lib *pluginbind.Library, d engine.Descriptor, launch pureGoLaunch, options engine.Options) (*pureGoSession, error) {
	if options.Verify == nil || options.Authorize == nil || (options.Reverse != nil && (options.Reverse.Handle == nil || options.Reverse.Authorize == nil)) {
		return nil, engine.ErrDenied
	}
	if options.Timeout < 0 {
		return nil, engine.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptor, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var requirements []json.RawMessage
	for _, r := range options.Requirements {
		value := map[string]any{"contract": r.Contract, "operation": r.Operation}
		if r.Identity != nil {
			value["identity"] = r.Identity
		}
		raw, _ := json.Marshal(value)
		requirements = append(requirements, raw)
	}
	s := &pureGoSession{descriptor: d.Clone(), options: options, base: context.WithoutCancel(ctx), done: make(chan struct{})}
	config := pluginbind.HostConfig{
		Descriptor: descriptor, Verify: "event", Authorize: "event", Observe: options.Observer != nil,
		Requirements: requirements,
		Backend:      pluginbind.HostConfigBackend{Kind: "process", Executable: launch.Executable, Arguments: launch.Arguments, Environment: launch.Environment},
	}
	handlers := pluginbind.HostHandlers{Verify: s.verify, Authorize: s.policy(func() func(context.Context, engine.Request) error { return options.Authorize })}
	if options.Observer != nil {
		handlers.Observe = s.observe
	}
	if r := options.Reverse; r != nil {
		config.Services = &pluginbind.HostConfigServices{MaxPayloadBytes: int64(r.MaxPayloadBytes), MaxInFlight: int64(r.MaxInFlight)}
		if r.Timeout > 0 {
			config.Services.TimeoutMS = timeoutMS(context.Background(), r.Timeout)
		}
		handlers.ReverseAuthorize = s.policy(func() func(context.Context, engine.Request) error { return r.Authorize })
		handlers.Reverse = s.reverse
	}
	if s.host, err = lib.OpenHost(config, handlers); err != nil {
		return nil, engineError(err)
	}
	if err := s.host.Start(pluginbind.HostStartInput{TimeoutMS: timeoutMS(ctx, options.Timeout)}); err != nil {
		s.mu.Lock()
		verifyErr := s.verifyErr
		s.mu.Unlock()
		_ = s.Abort()
		if verifyErr != nil && codeName(err) == "denied" {
			return nil, verifyErr
		}
		return nil, engineError(err)
	}
	return s, nil
}

func (s *pureGoSession) verify(data json.RawMessage) error {
	var d engine.Descriptor
	err := engine.Decode(data, &d)
	if err == nil {
		err = s.options.Verify(s.base, d)
	}
	s.mu.Lock()
	s.verifyErr = err
	s.mu.Unlock()
	return err
}

// policy answers a request with a Go policy, in the session's context bounded by the request's
// deadline.
func (s *pureGoSession) policy(f func() func(context.Context, engine.Request) error) func(json.RawMessage) error {
	return func(data json.RawMessage) error {
		var r engine.Request
		if err := engine.Decode(data, &r); err != nil {
			return err
		}
		ctx, cancel := context.WithDeadline(s.base, r.Deadline)
		defer cancel()
		return f()(ctx, r.Clone())
	}
}

func (s *pureGoSession) reverse(data json.RawMessage) (any, error) {
	var r engine.Request
	if err := engine.Decode(data, &r); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithDeadline(s.base, r.Deadline)
	defer cancel()
	payload, err := s.options.Reverse.Handle(ctx, r.Clone())
	if err != nil {
		return nil, err // a RemoteError marshals to {code, message} and is passed on
	}
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	return payload, nil
}

func (s *pureGoSession) observe(data json.RawMessage) {
	var o struct {
		Stage     string             `json:"stage"`
		Identity  engine.Identity    `json:"identity"`
		Contract  engine.ContractRef `json:"contract"`
		Operation string             `json:"operation"`
		RequestID string             `json:"requestID"`
		Code      string             `json:"code"`
		Duration  int64              `json:"durationMilliseconds"`
	}
	if engine.Decode(data, &o) == nil {
		s.options.Observer(s.base, engine.Event{Stage: o.Stage, Identity: o.Identity, Contract: o.Contract, Operation: o.Operation,
			RequestID: o.RequestID, Code: o.Code, Duration: time.Duration(o.Duration) * time.Millisecond})
	}
}

func (s *pureGoSession) Done() <-chan struct{} { return s.done }

func (s *pureGoSession) State() engine.State {
	state, err := s.host.State(nil)
	if err != nil {
		return engine.StateClosed
	}
	switch state {
	case "created", "ready":
		return engine.StateReady
	case "draining":
		return engine.StateDraining
	case "closed":
		return engine.StateClosed
	}
	return engine.StateFailed
}

func (s *pureGoSession) Stderr() string {
	text, _ := s.host.Stderr(nil)
	return text
}

func (s *pureGoSession) Call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	return s.call(ctx, contract, operation, payload, nil, engine.ProgressLimits{})
}

func (s *pureGoSession) CallProgress(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), options ...engine.CallOptions) (json.RawMessage, error) {
	var o engine.CallOptions
	if len(options) > 0 {
		o = options[0]
	}
	if err := o.Progress.Validate(); err != nil {
		return nil, err
	}
	return s.call(ctx, contract, operation, payload, onProgress, o.Progress)
}

func (s *pureGoSession) call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), limits engine.ProgressLimits) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := map[string]any{"contract": contract, "operation": operation}
	if len(payload) > 0 {
		call["payload"] = payload
	}
	raw, err := json.Marshal(call)
	if err != nil {
		return nil, err
	}
	in := pluginbind.HostCallInput{Call: raw, TimeoutMS: timeoutMS(ctx, s.options.Timeout)}
	type result struct {
		out json.RawMessage
		err error
	}
	finished := make(chan result, 1)
	go func() {
		var r result
		if onProgress != nil {
			in.Progress = &pluginbind.HostCallInputProgress{EventsPerSecond: int64(limits.EventsPerSecond), BytesPerSecond: int64(limits.BytesPerSecond)}
			r.out, r.err = s.host.CallWith(in, pluginbind.HostCallHandlers{Progress: func(data json.RawMessage) {
				var p engine.Progress
				if engine.Decode(data, &p) == nil {
					onProgress(p)
				}
			}})
		} else {
			r.out, r.err = s.host.Call(in)
		}
		finished <- r
	}()
	var r result
	select {
	case r = <-finished:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, engineError(r.err)
	}
	var response engine.Response
	if err := engine.Decode(r.out, &response); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, response.Error
	}
	return response.Payload, nil
}

func (s *pureGoSession) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.State() != engine.StateClosed {
		err := s.host.Drain(pluginbind.HostDrainInput{TimeoutMS: timeoutMS(ctx, s.options.Timeout)})
		if name := codeName(err); err != nil && name != "closed" && name != "not_found" {
			return engineError(err)
		}
	}
	return s.Abort()
}

func (s *pureGoSession) Abort() error {
	s.once.Do(func() {
		if err := s.host.Close(); err != nil && codeName(err) != "not_found" {
			s.closeErr = engineError(err)
		}
		close(s.done)
	})
	return s.closeErr
}
