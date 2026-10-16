//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"sync/atomic"
	"testing"
	"time"
)

// session is the part of a session both implementations share.
type session interface {
	Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)
	State() plugin.State
	Close(context.Context) error
	Abort() error
}

// causes lists the classes an error can belong to. Two implementations agree
// when an error is in exactly the same classes, whatever its text.
func causes(err error) string {
	if err == nil {
		return "ok"
	}
	var out string
	for _, c := range []struct {
		name string
		err  error
	}{
		{"invalid", plugin.ErrInvalid}, {"unsupported", plugin.ErrUnsupported}, {"notfound", plugin.ErrNotFound},
		{"ambiguous", plugin.ErrAmbiguous}, {"denied", plugin.ErrDenied}, {"mismatch", plugin.ErrMismatch},
		{"closed", plugin.ErrClosed}, {"draining", plugin.ErrDraining},
		{"canceled", context.Canceled}, {"deadline", context.DeadlineExceeded},
	} {
		if errors.Is(err, c.err) {
			out += c.name + ","
		}
	}
	var remote *plugin.RemoteError
	if errors.As(err, &remote) {
		out += "remote:" + remote.Code + ","
	}
	if out == "" {
		out = "other"
	}
	return out
}

type errorScenario struct {
	name    string
	backend func() *testBackend
	options func(*plugin.Options)
	run     func(t *testing.T, s session, ref plugin.ContractRef) []string
}

func one(ctx context.Context, s session, ref plugin.ContractRef, op string) string {
	_, err := s.Call(ctx, ref, op, nil)
	return causes(err)
}

func errorScenarios() []errorScenario {
	plain := func() *testBackend { return &testBackend{d: testDescriptor()} }
	failing := func(f func(context.Context, plugin.Request) (plugin.Response, error)) func() *testBackend {
		return func() *testBackend { return &testBackend{d: testDescriptor(), invoke: f} }
	}
	return []errorScenario{
		{name: "unknown-operation", backend: plain, run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			return []string{one(context.Background(), s, r, "nope")}
		}},
		{name: "unknown-contract", backend: plain, run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			return []string{one(context.Background(), s, plugin.ContractRef{Name: "other.work", Version: "v1"}, "inspect")}
		}},
		{name: "empty-operation", backend: plain, run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			return []string{one(context.Background(), s, r, "")}
		}},
		{name: "authorize-denies", backend: plain,
			options: func(o *plugin.Options) {
				o.Authorize = func(context.Context, plugin.Request) error { return errors.New("no") }
			},
			run: func(t *testing.T, s session, r plugin.ContractRef) []string {
				return []string{one(context.Background(), s, r, "inspect"), one(context.Background(), s, r, "inspect")}
			}},
		{name: "backend-error", backend: failing(func(context.Context, plugin.Request) (plugin.Response, error) {
			return plugin.Response{}, errors.New("boom")
		}), run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			return []string{one(context.Background(), s, r, "inspect"), one(context.Background(), s, r, "inspect")}
		}},
		{name: "wrong-response-id", backend: failing(func(_ context.Context, r plugin.Request) (plugin.Response, error) {
			return plugin.Response{APIVersion: plugin.APIVersion, ID: "other", Payload: json.RawMessage("null")}, nil
		}), run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			return []string{one(context.Background(), s, r, "inspect"), one(context.Background(), s, r, "inspect")}
		}},
		{name: "remote-error", backend: failing(func(_ context.Context, r plugin.Request) (plugin.Response, error) {
			return plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Error: &plugin.RemoteError{Code: "operation_failed", Message: "plugin operation failed"}}, nil
		}), run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			return []string{one(context.Background(), s, r, "inspect"), one(context.Background(), s, r, "inspect")}
		}},
		{name: "canceled-before-call", backend: plain, run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return []string{one(ctx, s, r, "inspect"), one(context.Background(), s, r, "inspect")}
		}},
		{name: "deadline-passed", backend: plain, run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			return []string{one(ctx, s, r, "inspect")}
		}},
		{name: "call-after-close", backend: plain, run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			first := causes(s.Close(context.Background()))
			return []string{first, one(context.Background(), s, r, "inspect"), causes(s.Close(context.Background()))}
		}},
		{name: "call-after-abort", backend: plain, run: func(t *testing.T, s session, r plugin.ContractRef) []string {
			first := causes(s.Abort())
			return []string{first, one(context.Background(), s, r, "inspect")}
		}},
	}
}

// TestSessionErrorCausesMatchGo drives identical backends through the C-backed
// session and the pure-Go session and requires the same error classes and the
// same final state for every call.
func TestSessionErrorCausesMatchGo(t *testing.T) {
	for _, sc := range errorScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			ref := testDescriptor().Contracts[0].ContractRef
			collect := func(open func(context.Context, plugin.Descriptor, plugin.Options) (session, error)) (string, *testBackend) {
				b := sc.backend()
				o := referenceOptions(b)
				if sc.options != nil {
					sc.options(&o)
				}
				s, err := open(context.Background(), b.d, o)
				if err != nil {
					return "open:" + causes(err), b
				}
				defer s.Abort()
				results := sc.run(t, s, ref)
				return fmt.Sprintf("%v state=%s closes=%d", results, s.State(), b.closes.Load()), b
			}
			goOut, _ := collect(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				return plugin.Open(ctx, d, o)
			})
			cOut, _ := collect(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				return Open(ctx, d, o)
			})
			if goOut != cOut {
				t.Fatalf("error causes differ\n  go: %s\n  c:  %s", goOut, cOut)
			}
		})
	}
}

// TestOpenErrorCausesMatchGo compares failures while admitting a plugin.
func TestOpenErrorCausesMatchGo(t *testing.T) {
	mismatched := testDescriptor()
	mismatched.Identity.Revision = "r2"
	cases := []struct {
		name    string
		backend func() *testBackend
		options func(*plugin.Options)
	}{
		{"verify-denies", func() *testBackend { return &testBackend{d: testDescriptor()} },
			func(o *plugin.Options) {
				o.Verify = func(context.Context, plugin.Descriptor) error { return errors.New("no") }
			}},
		{"connect-fails", func() *testBackend { return &testBackend{d: testDescriptor()} },
			func(o *plugin.Options) {
				o.Connect = func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return nil, errors.New("down") }
			}},
		{"handshake-mismatch", func() *testBackend { return &testBackend{d: mismatched} }, nil},
		{"nil-authorize", func() *testBackend { return &testBackend{d: testDescriptor()} },
			func(o *plugin.Options) { o.Authorize = nil }},
		{"nil-verify", func() *testBackend { return &testBackend{d: testDescriptor()} },
			func(o *plugin.Options) { o.Verify = nil }},
		{"nil-connect", func() *testBackend { return &testBackend{d: testDescriptor()} },
			func(o *plugin.Options) { o.Connect = nil }},
		{"canceled-context", func() *testBackend { return &testBackend{d: testDescriptor()} }, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(open func(context.Context, plugin.Descriptor, plugin.Options) (session, error)) string {
				b := c.backend()
				o := referenceOptions(b)
				if c.options != nil {
					c.options(&o)
				}
				ctx := context.Background()
				if c.name == "canceled-context" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				s, err := open(ctx, testDescriptor(), o)
				if s != nil {
					defer s.Abort()
				}
				return fmt.Sprintf("%s closes=%d", causes(err), b.closes.Load())
			}
			goOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				s, err := plugin.Open(ctx, d, o)
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			cOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				s, err := Open(ctx, d, o)
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			if goOut != cOut {
				t.Fatalf("open error causes differ\n  go: %s\n  c:  %s", goOut, cOut)
			}
		})
	}
}

// TestDrainErrorCausesMatchGo compares calls and closes while a call is in
// flight. Every probe waits for the event it depends on, so nothing here races
// a timer: drain with no deadline, then a close that times out.
func TestDrainErrorCausesMatchGo(t *testing.T) {
	type opener func(context.Context, plugin.Descriptor, plugin.Options) (session, error)
	rig := func(open opener) (s session, b *testBackend, ref plugin.ContractRef, started, release chan struct{}, inflight chan string) {
		release = make(chan struct{})
		started = make(chan struct{}, 1)
		var first atomic.Bool
		b = &testBackend{d: testDescriptor(), invoke: func(ctx context.Context, r plugin.Request) (plugin.Response, error) {
			if first.CompareAndSwap(false, true) { // only the first call blocks
				started <- struct{}{}
				<-release
			}
			return plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
		}}
		s, err := open(context.Background(), b.d, referenceOptions(b))
		if err != nil {
			t.Fatal(err)
		}
		ref = b.d.Contracts[0].ContractRef
		inflight = make(chan string, 1)
		go func() { inflight <- one(context.Background(), s, ref, "inspect") }()
		<-started
		return s, b, ref, started, release, inflight
	}
	drainWithoutDeadline := func(open opener) string {
		s, _, ref, _, release, inflight := rig(open)
		defer s.Abort()
		closed := make(chan string, 1)
		go func() { closed <- causes(s.Close(context.Background())) }()
		// Close with no deadline must be draining before it can finish; wait for that state.
		for i := 0; i < 2000 && s.State() != plugin.StateDraining; i++ {
			time.Sleep(time.Millisecond)
		}
		during := one(context.Background(), s, ref, "inspect")
		state := s.State()
		close(release)
		return fmt.Sprintf("inflight=%s during=%s close=%s state=%s final=%s", <-inflight, during, <-closed, state, s.State())
	}
	closeTimesOut := func(open opener) string {
		s, _, ref, _, release, inflight := rig(open)
		defer s.Abort()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		timedOut := causes(s.Close(ctx)) // returns only once the deadline has passed
		after := one(context.Background(), s, ref, "inspect")
		state := s.State()
		close(release)
		return fmt.Sprintf("close=%s after=%s state=%s inflight=%s final=%s", timedOut, after, state, <-inflight, s.State())
	}
	for name, run := range map[string]func(opener) string{"drain-without-deadline": drainWithoutDeadline, "close-times-out": closeTimesOut} {
		t.Run(name, func(t *testing.T) {
			goOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				return plugin.Open(ctx, d, o)
			})
			cOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				return Open(ctx, d, o)
			})
			if goOut != cOut {
				t.Fatalf("drain error causes differ\n  go: %s\n  c:  %s", goOut, cOut)
			}
		})
	}
}
