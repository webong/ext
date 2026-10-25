//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
)

// The reverse-call comparison runs one guest through the pure-Go session and the
// C engine, with host services that record what they see, and compares what the
// guest got back, how the call ended and everything the host's policy was shown.

type reverseSession interface {
	Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)
	State() plugin.State
	Abort() error
}

type ctxKeyType struct{}

var ctxKey ctxKeyType

// recorder notes what the services were shown. Requests are normalized: the
// deadline is the guest's own clock, so only whether it was set is kept.
type recorder struct {
	mu                         sync.Mutex
	Authorized, Handled, Facts []string
	callDeadline               time.Time
	release                    chan struct{} // closed to let "hold" return
}

func (rec *recorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.Handled)
}

func (rec *recorder) fact(f string) {
	rec.mu.Lock()
	rec.Facts = append(rec.Facts, f)
	rec.mu.Unlock()
}

func (rec *recorder) hasFact(f string) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, have := range rec.Facts {
		if have == f {
			return true
		}
	}
	return false
}

// eventually polls cond for up to three seconds.
func eventually(cond func() bool) bool {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func normalized(r plugin.Request) string {
	set := !r.Deadline.IsZero()
	r.Deadline = time.Time{}
	raw, _ := json.Marshal(r)
	return fmt.Sprintf("%s deadline-set=%v", raw, set)
}

func (rec *recorder) options(timeout time.Duration, maxPayload int) *plugin.ReverseOptions {
	return &plugin.ReverseOptions{
		Timeout:         timeout,
		MaxPayloadBytes: maxPayload,
		Authorize: func(ctx context.Context, r plugin.Request) error {
			rec.mu.Lock()
			rec.Authorized = append(rec.Authorized, normalized(r))
			rec.mu.Unlock()
			if r.Operation == "secret" {
				return errors.New("not for this plugin")
			}
			return nil
		},
		Handle: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
			rec.mu.Lock()
			rec.Handled = append(rec.Handled, normalized(r))
			rec.mu.Unlock()
			deadline, ok := ctx.Deadline()
			rec.fact(fmt.Sprintf("deadline-within-call=%v value=%v", ok && !deadline.After(rec.callDeadline), ctx.Value(ctxKey)))
			switch r.Operation {
			case "sign":
				return json.RawMessage(`"signed"`), nil
			case "empty":
				return nil, nil
			case "fail":
				return nil, errors.New("boom")
			case "errok":
				return nil, &plugin.RemoteError{Code: "policy", Message: "refused for you", RetryAfterMilliseconds: 5}
			case "errcode":
				return nil, &plugin.RemoteError{Code: "Policy", Message: "x"}
			case "errmsg":
				return nil, &plugin.RemoteError{Code: "policy", Message: strings.Repeat("a", 4097)}
			case "errretry":
				return nil, &plugin.RemoteError{Code: "policy", Message: "x", RetryAfterMilliseconds: 31536000001}
			case "errretryok":
				return nil, &plugin.RemoteError{Code: "policy", Message: "x", RetryAfterMilliseconds: 31536000000}
			case "badjson":
				return json.RawMessage(`{bad`), nil
			case "big":
				return json.RawMessage(`"` + strings.Repeat("0123456789", 7) + `"`), nil
			case "slow":
				<-ctx.Done()
				return nil, ctx.Err()
			case "slowok":
				time.Sleep(450 * time.Millisecond)
				return json.RawMessage(`"late"`), nil
			case "hold":
				select {
				case <-rec.release:
					return json.RawMessage(`"held"`), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case "hang":
				<-ctx.Done()
				rec.fact("canceled=true")
				return nil, ctx.Err()
			case "nested":
				// A host that is itself a plugin answers by calling its own host.
				upstream := plugin.ReverseFrom(ctx)
				rec.mu.Lock()
				rec.Facts = append(rec.Facts, fmt.Sprintf("upstream=%v value=%v", upstream != nil, ctx.Value(ctxKey)))
				rec.mu.Unlock()
				if upstream == nil {
					return nil, errors.New("lost the upstream caller")
				}
				return upstream.Call(ctx, hostRef, "sign", r.Payload)
			}
			return nil, errors.New("unknown operation")
		},
	}
}

type upstreamCaller struct{}

func (upstreamCaller) Call(_ context.Context, _ plugin.ContractRef, op string, payload json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`"from-top:` + op + `"`), nil
}

func reverseOpts() plugin.Options {
	options := progressOptions()
	options.Timeout = 20 * time.Second
	return options
}

func openGoReverse(t *testing.T, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	return openGoReverseIn(t, context.Background(), mode, reverse)
}

func openGoReverseIn(t *testing.T, ctx context.Context, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	t.Helper()
	exe := guestPath(t)
	options := reverseOpts()
	options.Reverse = reverse
	options.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: exe, Env: []string{guestEnv + "=" + mode}})
	}
	s, err := plugin.Open(ctx, reverseDescriptor(), options)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func openCReverse(t *testing.T, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	return openCReverseIn(t, context.Background(), mode, reverse)
}

func openCReverseIn(t *testing.T, ctx context.Context, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	t.Helper()
	launch := LaunchOptions{ProcessOptions: ProcessOptions{Executable: guestPath(t), Environment: []string{guestEnv + "=" + mode}}}
	options := reverseOpts()
	options.Reverse = reverse
	s, err := OpenProcess(ctx, reverseDescriptor(), launch, options)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type reverseOutcome struct {
	Open, Result, Err          string
	State                      plugin.State
	Authorized, Handled, Facts []string
}

type reverseCase struct {
	name, mode, payload string
	noReverse           bool
	nested              bool
	want                string // a substring of the result or the error class
}

func (c reverseCase) run(t *testing.T, open func(*testing.T, string, *plugin.ReverseOptions) (reverseSession, error)) reverseOutcome {
	t.Helper()
	rec := &recorder{}
	var reverse *plugin.ReverseOptions
	if !c.noReverse {
		reverse = rec.options(200*time.Millisecond, 64)
	}
	s, err := open(t, c.mode, reverse)
	if err != nil {
		return reverseOutcome{Open: causes(err)}
	}
	defer s.Abort()
	ctx := context.WithValue(context.Background(), ctxKey, "root")
	if c.nested {
		ctx = plugin.WithReverse(ctx, upstreamCaller{})
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rec.callDeadline, _ = ctx.Deadline()
	result, err := s.Call(ctx, reverseRef, "call", json.RawMessage(c.payload))
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return reverseOutcome{Result: string(result), Err: causes(err), State: s.State(), Authorized: rec.Authorized, Handled: rec.Handled, Facts: rec.Facts}
}

func goCall(op string) string {
	return `{"op":"` + op + `","payload":{"n":1}}`
}

func reverseCases() []reverseCase {
	return []reverseCase{
		// A Go guest calling through plugin.ReverseFrom.
		{name: "answer", mode: "rev-go", payload: goCall("sign"), want: `"result":"signed"`},
		{name: "empty payload is null", mode: "rev-go", payload: goCall("empty"), want: `"result":null`},
		{name: "denied by Authorize", mode: "rev-go", payload: goCall("secret"), want: `"code":"denied"`},
		{name: "handler error", mode: "rev-go", payload: goCall("fail"), want: `"code":"operation_failed"`},
		{name: "public error", mode: "rev-go", payload: goCall("errok"), want: `"code":"policy"`},
		{name: "invalid code", mode: "rev-go", payload: goCall("errcode"), want: `"code":"operation_failed"`},
		{name: "over-long message", mode: "rev-go", payload: goCall("errmsg"), want: `"code":"operation_failed"`},
		{name: "retry hint over a year", mode: "rev-go", payload: goCall("errretry"), want: `"code":"operation_failed"`},
		{name: "retry hint of a year", mode: "rev-go", payload: goCall("errretryok"), want: `"retry":31536000000`},
		{name: "answer is not JSON", mode: "rev-go", payload: goCall("badjson"), want: `"code":"operation_failed"`},
		{name: "answer over the bound", mode: "rev-go", payload: goCall("big"), want: `"code":"too_large"`},
		{name: "handler outlives the deadline", mode: "rev-go", payload: goCall("slow"), want: `"code":"operation_failed"`},
		{name: "answer after the deadline", mode: "rev-go", payload: goCall("slowok"), want: `"code":"operation_failed"`},
		{name: "nested hosting", mode: "rev-go", payload: goCall("nested"), nested: true, want: `"result":"from-top:sign"`},
		// Hand-made frames: what the Go guest cannot ask for.
		{name: "undeclared operation", mode: "rev-raw:undeclared", payload: `{}`, want: `"code":"reverse_undeclared"`},
		{name: "plugin's deadline passed", mode: "rev-raw:expired", payload: `{}`, want: `"code":"expired"`},
		{name: "request over the bound", mode: "rev-raw:bigrequest", payload: `{}`, want: `"code":"too_large"`},
		// Without services every reverse call is unavailable, whatever it names.
		{name: "no services", mode: "rev-go", payload: goCall("sign"), noReverse: true, want: `"code":"reverse_unavailable"`},
		{name: "no services, undeclared", mode: "rev-raw:undeclared", payload: `{}`, noReverse: true, want: `"code":"reverse_unavailable"`},
		{name: "no services, wrong parent", mode: "rev-raw:wrongparent", payload: `{}`, noReverse: true, want: `"code":"reverse_unavailable"`},
		{name: "no services, session scoped", mode: "rev-raw:noparent", payload: `{}`, noReverse: true, want: `"code":"reverse_unavailable"`},
		{name: "no services, before the handshake", mode: "rev-raw:pre-handshake", payload: `{}`, noReverse: true, want: `"reply":null`},
		{name: "session scoped, before the handshake", mode: "rev-raw:pre-handshake-session", payload: `{}`, want: `"code":"reverse_unavailable"`},
		{name: "session scoped, during a call", mode: "rev-raw:noparent", payload: `{}`, want: `"payload":"signed"`},
		// Breaking the framing ends the session.
		{name: "parent is not the call in flight", mode: "rev-raw:wrongparent", payload: `{}`, want: "protocol,"},
		{name: "deadline is not a time", mode: "rev-raw:bad-deadline", payload: `{}`, want: "protocol,"},
		{name: "id is not an identifier", mode: "rev-raw:bad-id", payload: `{}`, want: "protocol,"},
		{name: "unknown field", mode: "rev-raw:bad-extra", payload: `{}`, want: "protocol,"},
		{name: "operation is not an identifier", mode: "rev-raw:bad-operation", payload: `{}`, want: "protocol,"},
		{name: "call before the handshake", mode: "rev-raw:pre-handshake", payload: `{}`, want: "protocol,"},
	}
}

func TestReverseCallsMatchGo(t *testing.T) {
	for _, c := range reverseCases() {
		t.Run(c.name, func(t *testing.T) {
			want := c.run(t, openGoReverse)
			got := c.run(t, openCReverse)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("C engine and Go session disagree\n  C:  %+v\n  Go: %+v", got, want)
			}
			// Both engines could be wrong in the same way: pin the answer.
			if text := want.Result + want.Err + want.Open; !strings.Contains(text, c.want) {
				t.Errorf("the Go session answered %+v, want %q", want, c.want)
			}
		})
	}
}

// The same plugins over a Go backend: the engine decides, the backend carries.
func TestBackendReverseCallsMatchGo(t *testing.T) {
	for _, c := range reverseCases() {
		t.Run(c.name, func(t *testing.T) {
			want := c.run(t, openGoReverse)
			got := c.run(t, openCBackendReverse)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("engine over a backend and Go session disagree\n  C:  %+v\n  Go: %+v", got, want)
			}
		})
	}
}

// A plugin in the host's own process, which calls the services directly.
func TestInProcessReverseCallsMatchGo(t *testing.T) {
	for _, c := range reverseCases() {
		if c.mode != "rev-go" {
			continue // a hand-made frame needs a process
		}
		t.Run(c.name, func(t *testing.T) {
			open := func(openIn openIn) func(*testing.T, string, *plugin.ReverseOptions) (reverseSession, error) {
				return func(t *testing.T, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
					return openIn(t, context.Background(), mode, reverse)
				}
			}
			want := c.run(t, open(openGoInProcessReverseIn))
			got := c.run(t, open(openCInProcessReverseIn))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("engine and Go session disagree\n  C:  %+v\n  Go: %+v", got, want)
			}
		})
	}
}

// The request the services are shown is the plugin's frame as the host's own
// record of the call, and the same in both engines.
func TestReverseRequestSeenByServices(t *testing.T) {
	c := reverseCase{mode: "rev-go", payload: goCall("sign")}
	got := c.run(t, openCReverse)
	want := `{"apiVersion":"ext.plugin/v1","id":"g1","plugin":{"id":"cgoengine/reverse","revision":"r1"},"contract":{"name":"host.svc","version":"v1"},"operation":"sign","surface":"signing","deadline":"0001-01-01T00:00:00Z","payload":{"n":1},"parent":"1"} deadline-set=true`
	if len(got.Authorized) != 1 || len(got.Handled) != 1 || got.Authorized[0] != want || got.Handled[0] != want {
		t.Fatalf("authorized %q, handled %q, want %q", got.Authorized, got.Handled, want)
	}
	if !reflect.DeepEqual(got.Facts, []string{"deadline-within-call=true value=root"}) {
		t.Fatalf("facts %q", got.Facts)
	}
	// A refused call never reaches the services.
	for _, mode := range []string{"rev-raw:undeclared", "rev-raw:expired", "rev-raw:bigrequest"} {
		if got := (reverseCase{mode: mode, payload: `{}`}).run(t, openCReverse); len(got.Authorized) != 0 || len(got.Handled) != 0 {
			t.Errorf("%s reached the services: %+v", mode, got)
		}
	}
	// Denied by Authorize: the handler is not called.
	if got := (reverseCase{mode: "rev-go", payload: goCall("secret")}).run(t, openCReverse); len(got.Authorized) != 1 || len(got.Handled) != 0 {
		t.Errorf("denied call: %+v", got)
	}
}

// ReverseOptions are checked the same way before anything starts.
func TestReverseOptionsMatchGo(t *testing.T) {
	good := func() *plugin.ReverseOptions { return (&recorder{}).options(0, 0) }
	mutate := func(f func(*plugin.ReverseOptions)) *plugin.ReverseOptions {
		o := good()
		f(o)
		return o
	}
	for name, o := range map[string]*plugin.ReverseOptions{
		"valid":                  good(),
		"no handler":             mutate(func(o *plugin.ReverseOptions) { o.Handle = nil }),
		"no authorize":           mutate(func(o *plugin.ReverseOptions) { o.Authorize = nil }),
		"negative timeout":       mutate(func(o *plugin.ReverseOptions) { o.Timeout = -1 }),
		"negative payload bound": mutate(func(o *plugin.ReverseOptions) { o.MaxPayloadBytes = -1 }),
		"payload over frame":     mutate(func(o *plugin.ReverseOptions) { o.MaxPayloadBytes = plugin.MaxFrameBytes + 1 }),
		"largest payload":        mutate(func(o *plugin.ReverseOptions) { o.MaxPayloadBytes = plugin.MaxFrameBytes }),
		"negative in flight":     mutate(func(o *plugin.ReverseOptions) { o.MaxInFlight = -1 }),
		"in flight over limit":   mutate(func(o *plugin.ReverseOptions) { o.MaxInFlight = 1025 }),
		"largest in flight":      mutate(func(o *plugin.ReverseOptions) { o.MaxInFlight = 1024 }),
		"very long timeout":      mutate(func(o *plugin.ReverseOptions) { o.Timeout = 1000 * time.Hour }),
	} {
		t.Run(name, func(t *testing.T) {
			var classes [3]string
			for i, open := range []func(*testing.T, string, *plugin.ReverseOptions) (reverseSession, error){openGoReverse, openCReverse, openCBackendReverse} {
				s, err := open(t, "rev-go", o)
				if err == nil {
					_ = s.Abort()
				}
				classes[i] = causes(err)
			}
			if classes[0] != classes[1] || classes[0] != classes[2] {
				t.Errorf("Go session %q, C engine %q, C engine over a backend %q", classes[0], classes[1], classes[2])
			}
		})
	}
}
