package jsonline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/inprocess"
)

var (
	svc    = plugin.ContractRef{Name: "host.svc", Version: "v1"}
	main1  = plugin.ContractRef{Name: "work", Version: "v1"}
	ctxKey struct{ name string }
)

func reverseDescriptor() plugin.Descriptor {
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "guest", Revision: "r1"},
		Contracts: []plugin.Contract{{ContractRef: main1, Operations: []plugin.Operation{
			{Name: "run"}, {Name: "long", Progress: true}, {Name: "quiet"},
		}}},
		Reverse: []plugin.Contract{{ContractRef: svc, Operations: []plugin.Operation{{Name: "sign"}, {Name: "slow"}}}},
	}
}

type rig struct {
	session *plugin.Session
	stop    func()
}

func open(t *testing.T, ctx context.Context, d plugin.Descriptor, handler plugin.Handler, reverse *plugin.ReverseOptions) *rig {
	t.Helper()
	host, server := net.Pipe()
	guest, err := plugin.NewGuest(d, plugin.GuestOptions{Handler: handler, MaxCallDuration: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- ServeGuest(ctx, server, guest) }()
	session, err := plugin.Open(ctx, d, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return NewClient(host), nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Timeout:   5 * time.Second,
		Reverse:   reverse,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{session: session, stop: func() { _ = session.Abort(); <-served }}
}

func allow(handle plugin.Handler) *plugin.ReverseOptions {
	return &plugin.ReverseOptions{Handle: handle, Authorize: func(context.Context, plugin.Request) error { return nil }}
}

func remoteCode(err error) string {
	var remote *plugin.RemoteError
	if errors.As(err, &remote) {
		return remote.Code
	}
	return ""
}

func TestReverseCallRoundTrip(t *testing.T) {
	var sawParent string
	var sawValue any
	reverse := allow(func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		sawParent, sawValue = r.Parent, ctx.Value(ctxKey)
		return json.RawMessage(`{"signed":` + string(r.Payload) + `}`), nil
	})
	rootCtx := context.WithValue(context.Background(), ctxKey, "root")
	r := open(t, rootCtx, reverseDescriptor(), func(ctx context.Context, req plugin.Request) (json.RawMessage, error) {
		caller := plugin.ReverseFrom(ctx)
		if caller == nil {
			return nil, errors.New("no reverse caller")
		}
		return caller.Call(ctx, svc, "sign", json.RawMessage(`"x"`))
	}, reverse)
	defer r.stop()
	got, err := r.session.Call(rootCtx, main1, "run", nil)
	if err != nil || string(got) != `{"signed":"x"}` {
		t.Fatalf("got %s, %v", got, err)
	}
	if sawParent != "1" {
		t.Fatalf("reverse call must name the forward call it belongs to, got %q", sawParent)
	}
	if sawValue != "root" {
		t.Fatalf("the handler context must derive from the call's context, value %v", sawValue)
	}
}

func TestReverseDeniedPaths(t *testing.T) {
	var reached atomic.Int32
	host := allow(func(context.Context, plugin.Request) (json.RawMessage, error) {
		reached.Add(1)
		return json.RawMessage(`1`), nil
	})
	call := func(ref plugin.ContractRef, op string, payload string) plugin.Handler {
		return func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
			return plugin.ReverseFrom(ctx).Call(ctx, ref, op, json.RawMessage(payload))
		}
	}
	// A guest cannot call what its descriptor did not declare: the guest itself
	// refuses before sending.
	r := open(t, context.Background(), reverseDescriptor(), call(svc, "undeclared", `1`), host)
	if _, err := r.session.Call(context.Background(), main1, "run", nil); remoteCode(err) != plugin.ReverseUndeclared {
		t.Fatalf("undeclared operation: %v", err)
	}
	r.stop()
	// No Reverse options on the host: every reverse call is denied and the
	// session continues.
	r = open(t, context.Background(), reverseDescriptor(), call(svc, "sign", `1`), nil)
	if _, err := r.session.Call(context.Background(), main1, "run", nil); remoteCode(err) != plugin.ReverseUnavailable {
		t.Fatalf("no host services must be told apart from a refusal: %v", err)
	}
	if r.session.State() != plugin.StateReady {
		t.Fatalf("a denied reverse call must not fail the session, state %s", r.session.State())
	}
	r.stop()
	// Authorize says no.
	refuse := allow(func(context.Context, plugin.Request) (json.RawMessage, error) { reached.Add(1); return nil, nil })
	refuse.Authorize = func(context.Context, plugin.Request) error { return errors.New("no") }
	r = open(t, context.Background(), reverseDescriptor(), call(svc, "sign", `1`), refuse)
	if _, err := r.session.Call(context.Background(), main1, "run", nil); remoteCode(err) != plugin.ReverseDenied {
		t.Fatalf("authorize was ignored or mislabeled: %v", err)
	}
	r.stop()
	if reached.Load() != 0 {
		t.Fatalf("a refused reverse call reached the handler %d times", reached.Load())
	}
	// Too large.
	small := allow(func(context.Context, plugin.Request) (json.RawMessage, error) { return json.RawMessage(`1`), nil })
	small.MaxPayloadBytes = 8
	r = open(t, context.Background(), reverseDescriptor(), call(svc, "sign", `"`+strings.Repeat("a", 64)+`"`), small)
	defer r.stop()
	if _, err := r.session.Call(context.Background(), main1, "run", nil); err == nil || !strings.Contains(err.Error(), "refused") && remoteCode(err) == "" {
		t.Fatalf("oversize payload: %v", err)
	}
}

func TestReverseBusy(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	host := allow(func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(`1`), nil
	})
	host.MaxInFlight = 1
	var busy atomic.Int32
	r := open(t, context.Background(), reverseDescriptor(), func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		caller := plugin.ReverseFrom(ctx)
		first := make(chan error, 1)
		go func() { _, err := caller.Call(ctx, svc, "slow", nil); first <- err }()
		<-entered
		_, err := caller.Call(ctx, svc, "sign", nil)
		if remoteCode(err) == plugin.ReverseBusy {
			busy.Add(1)
		}
		close(release)
		if err := <-first; err != nil {
			return nil, err
		}
		return json.RawMessage(`1`), nil
	}, host)
	defer r.stop()
	if _, err := r.session.Call(context.Background(), main1, "run", nil); err != nil {
		t.Fatal(err)
	}
	if busy.Load() != 1 {
		t.Fatal("the second concurrent reverse call must be busy")
	}
}

func TestSessionScopedReverseCallAfterTheCallReturned(t *testing.T) {
	background := make(chan error, 1)
	var caller plugin.ReverseCaller
	var mu sync.Mutex
	var scopes []string
	host := allow(func(_ context.Context, r plugin.Request) (json.RawMessage, error) {
		mu.Lock()
		scopes = append(scopes, r.Parent)
		mu.Unlock()
		return json.RawMessage(`"ok"`), nil
	})
	r := open(t, context.Background(), reverseDescriptor(), func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		caller = plugin.SessionReverseFrom(ctx)
		return json.RawMessage(`1`), nil
	}, host)
	defer r.stop()
	if _, err := r.session.Call(context.Background(), main1, "run", nil); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := caller.Call(context.Background(), svc, "sign", nil) // no call is in flight
		background <- err
	}()
	select {
	case err := <-background:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a session-scoped reverse call was not served while idle")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(scopes) != 1 || scopes[0] != "" {
		t.Fatalf("a session-scoped call has no parent, got %q", scopes)
	}
}

func TestReverseHandlerEndsWithItsCall(t *testing.T) {
	canceled := make(chan struct{})
	started := make(chan struct{})
	host := allow(func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	r := open(t, context.Background(), reverseDescriptor(), func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		go func() { _, _ = plugin.ReverseFrom(ctx).Call(context.Background(), svc, "slow", nil) }()
		<-started
		return json.RawMessage(`1`), nil // returns while the reverse call is still running
	}, host)
	defer r.stop()
	if _, err := r.session.Call(context.Background(), main1, "run", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("a call-scoped reverse handler outlived its call")
	}
}

// rawGuest speaks the guest side by hand so tests can send frames a correct
// guest never would.
func rawGuest(t *testing.T, d plugin.Descriptor, reverse *plugin.ReverseOptions) (*plugin.Session, *bufio.Reader, net.Conn) {
	t.Helper()
	host, guest := net.Pipe()
	reader := bufio.NewReader(guest)
	go func() {
		line, err := readFrame(reader)
		if err != nil {
			return
		}
		var hello plugin.Request
		_ = plugin.Decode(line, &hello)
		payload, _ := json.Marshal(d)
		data, _ := json.Marshal(plugin.Response{APIVersion: plugin.APIVersion, ID: hello.ID, Payload: payload})
		_ = writeFrame(guest, data)
	}()
	session, err := plugin.Open(context.Background(), d, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return NewClient(host), nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Timeout:   5 * time.Second,
		Reverse:   reverse,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session, reader, guest
}

func sendFrame(t *testing.T, w net.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(w, data); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolViolationsFailTheSession(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	cases := map[string]any{
		"reverse request naming a call that is not in flight": plugin.ReverseRequestFrame{Frame: plugin.FrameReverseRequest, APIVersion: plugin.APIVersion, ID: "g1", Parent: "99", Contract: svc, Operation: "sign", Deadline: deadline},
		"progress with no call":                               plugin.ProgressFrame{Frame: plugin.FrameProgress, APIVersion: plugin.APIVersion, Parent: "1", Progress: plugin.Progress{Kind: "log", Text: "x"}},
		"unknown frame":                                       map[string]any{"frame": "surprise"},
		"unsolicited response":                                plugin.Response{APIVersion: plugin.APIVersion, ID: "5", Payload: json.RawMessage(`1`)},
		"malformed reverse request":                           map[string]any{"frame": plugin.FrameReverseRequest, "apiVersion": plugin.APIVersion},
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			session, _, guest := rawGuest(t, reverseDescriptor(), allow(func(context.Context, plugin.Request) (json.RawMessage, error) { return json.RawMessage(`1`), nil }))
			defer guest.Close()
			sendFrame(t, guest, frame)
			// The guest never answers, so a call can only end by the violation
			// failing the connection; it must do so well inside the timeout.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			start := time.Now()
			_, err := session.Call(ctx, main1, "run", nil)
			if err == nil {
				t.Fatal("the session kept working after a protocol violation")
			}
			if time.Since(start) > 2*time.Second {
				t.Fatalf("the violation was not detected promptly: %v", err)
			}
		})
	}
}

func TestProgressDeclaredAndDelivered(t *testing.T) {
	r := open(t, context.Background(), reverseDescriptor(), func(ctx context.Context, req plugin.Request) (json.RawMessage, error) {
		p := plugin.ProgressFrom(ctx)
		if req.Operation == "quiet" {
			if err := p.Report(plugin.Progress{Kind: "log", Text: "nope"}); !errors.Is(err, plugin.ErrUnsupported) {
				return nil, errors.New("an operation that did not declare progress could report")
			}
			return json.RawMessage(`1`), nil
		}
		_ = p.Report(plugin.Progress{Kind: "progress", Text: "step", Completed: 1, Total: 4})
		_ = p.Report(plugin.Progress{Kind: "log", Stream: "err", Text: "warning"})
		return json.RawMessage(`"done"`), nil
	}, nil)
	defer r.stop()
	var events []plugin.Progress
	got, err := r.session.CallProgress(context.Background(), main1, "long", nil, func(p plugin.Progress) { events = append(events, p) })
	if err != nil || string(got) != `"done"` {
		t.Fatalf("%s %v", got, err)
	}
	if len(events) != 2 || events[0].Completed != 1 || events[0].Total != 4 || events[1].Stream != "err" || events[1].Text != "warning" {
		t.Fatalf("events %+v", events)
	}
	if _, err := r.session.CallProgress(context.Background(), main1, "quiet", nil, func(plugin.Progress) { t.Error("callback for an undeclared operation") }); err != nil {
		t.Fatal(err)
	}
	// Call discards events.
	if _, err := r.session.Call(context.Background(), main1, "long", nil); err != nil {
		t.Fatal(err)
	}
}

func TestProgressOverTheRateIsDroppedAndCounted(t *testing.T) {
	var reported, refused atomic.Int32
	r := open(t, context.Background(), reverseDescriptor(), func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		p := plugin.ProgressFrom(ctx)
		for i := 0; i < 200; i++ {
			switch err := p.Report(plugin.Progress{Kind: "log", Text: "line"}); {
			case err == nil:
				reported.Add(1)
			case errors.Is(err, plugin.ErrProgressDropped):
				refused.Add(1)
			default:
				return nil, err
			}
		}
		return json.RawMessage(`1`), nil
	}, nil)
	defer r.stop()
	var events, markers, dropped uint64
	_, err := r.session.CallProgress(context.Background(), main1, "long", nil, func(p plugin.Progress) {
		if p.Text == "" {
			markers++ // the final marker carries a count and no text
		} else {
			events++
		}
		dropped += p.Dropped
	}, plugin.CallOptions{Progress: plugin.ProgressLimits{EventsPerSecond: 10}})
	if err != nil {
		t.Fatalf("a flood of events must never fail the call: %v", err)
	}
	if refused.Load() == 0 {
		t.Fatal("the sender was never limited")
	}
	// Every event is either delivered or counted as dropped, none lost.
	if events+dropped != 200 {
		t.Fatalf("delivered %d + dropped %d != 200 (markers %d)", events, dropped, markers)
	}
	if markers != 1 {
		t.Fatalf("expected one final marker, got %d", markers)
	}
}

func TestOutstandingProgressNeverFollowsTheResponse(t *testing.T) {
	// A reporter used after its call returned must fail, not send a frame that
	// the host would take for a violation.
	var late plugin.ProgressReporter
	r := open(t, context.Background(), reverseDescriptor(), func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		late = plugin.ProgressFrom(ctx)
		return json.RawMessage(`1`), nil
	}, nil)
	defer r.stop()
	if _, err := r.session.CallProgress(context.Background(), main1, "long", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := late.Report(plugin.Progress{Kind: "log", Text: "late"}); !errors.Is(err, plugin.ErrClosed) {
		t.Fatalf("late report: %v", err)
	}
	if r.session.State() != plugin.StateReady {
		t.Fatalf("state %s", r.session.State())
	}
	if _, err := r.session.Call(context.Background(), main1, "run", nil); err != nil {
		t.Fatal(err)
	}
}

// openInProcess opens a session over the in-process backend.
func openInProcess(t *testing.T, ctx context.Context, d plugin.Descriptor, handler plugin.Handler, reverse *plugin.ReverseOptions) *plugin.Session {
	t.Helper()
	guest, err := plugin.NewGuest(d, plugin.GuestOptions{Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := inprocess.New(guest)
	if err != nil {
		t.Fatal(err)
	}
	session, err := plugin.Open(ctx, d, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Reverse:   reverse,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Abort() })
	return session
}

func TestInProcessReverseAndProgress(t *testing.T) {
	session := openInProcess(t, context.Background(), reverseDescriptor(), func(ctx context.Context, req plugin.Request) (json.RawMessage, error) {
		_ = plugin.ProgressFrom(ctx).Report(plugin.Progress{Kind: "log", Stream: "out", Text: "hi"})
		return plugin.ReverseFrom(ctx).Call(ctx, svc, "sign", json.RawMessage(`7`))
	}, allow(func(_ context.Context, r plugin.Request) (json.RawMessage, error) {
		return json.RawMessage(`{"n":` + string(r.Payload) + `}`), nil
	}))
	var texts []string
	got, err := session.CallProgress(context.Background(), main1, "long", nil, func(p plugin.Progress) { texts = append(texts, p.Text) })
	if err != nil || string(got) != `{"n":7}` {
		t.Fatalf("%s %v", got, err)
	}
	if len(texts) != 1 || texts[0] != "hi" {
		t.Fatalf("progress %q", texts)
	}
	// Undeclared reverse operations are refused in this binding too.
	denied := openInProcess(t, context.Background(), reverseDescriptor(), func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		return plugin.ReverseFrom(ctx).Call(ctx, svc, "undeclared", nil)
	}, allow(func(context.Context, plugin.Request) (json.RawMessage, error) { return json.RawMessage(`1`), nil }))
	if _, err := denied.Call(context.Background(), main1, "run", nil); remoteCode(err) != plugin.ReverseUndeclared {
		t.Fatalf("undeclared: %v", err)
	}
}

// A guest that is itself a host answers its child's reverse call by calling its
// own host: child -> middle -> top, with one context chain.
func TestNestedHosting(t *testing.T) {
	var topSaw atomic.Value
	top := allow(func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		deadline, _ := ctx.Deadline()
		topSaw.Store(deadline)
		return json.RawMessage(`"from-top:` + strings.Trim(string(r.Payload), `"`) + `"`), nil
	})
	rootCtx := context.WithValue(context.Background(), ctxKey, "root")
	middle := func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		// This handler hosts a child plugin and serves its reverse calls by calling up.
		childDescriptor := reverseDescriptor()
		child := openInProcess(t, ctx, childDescriptor, func(cctx context.Context, _ plugin.Request) (json.RawMessage, error) {
			return plugin.ReverseFrom(cctx).Call(cctx, svc, "sign", json.RawMessage(`"secret"`))
		}, allow(func(hctx context.Context, r plugin.Request) (json.RawMessage, error) {
			upstream := plugin.ReverseFrom(hctx)
			if upstream == nil {
				return nil, errors.New("the child's reverse handler lost its upstream caller")
			}
			if hctx.Value(ctxKey) != "root" {
				return nil, errors.New("context values did not carry through the chain")
			}
			return upstream.Call(hctx, svc, "sign", r.Payload)
		}))
		return child.Call(ctx, main1, "run", nil)
	}
	r := open(t, rootCtx, reverseDescriptor(), middle, top)
	defer r.stop()
	callCtx, cancel := context.WithTimeout(rootCtx, time.Minute)
	defer cancel()
	got, err := r.session.Call(callCtx, main1, "run", nil)
	if err != nil || string(got) != `"from-top:secret"` {
		t.Fatalf("%s %v", got, err)
	}
	want, _ := callCtx.Deadline()
	if seen, _ := topSaw.Load().(time.Time); seen.IsZero() || seen.After(want) {
		t.Fatalf("deadlines must nest child < middle < top: top saw %v, call %v", seen, want)
	}
}

// A handler's own RemoteError reaches the guest as a *plugin.RemoteError with
// the same code and message. One the receiver would reject (an invalid code, or
// a message over the bound) becomes operation_failed, and never ends the
// connection: a handler bug must not take the plugin down.
func TestReverseHandlerRemoteErrors(t *testing.T) {
	cases := []struct {
		name     string
		remote   *plugin.RemoteError
		wantCode string
		wantText string
	}{
		{"custom code and message pass through", &plugin.RemoteError{Code: "policy", Message: "refused for ******"}, "policy", "refused for ******"},
		{"uppercase code", &plugin.RemoteError{Code: "Policy", Message: "x"}, "operation_failed", "host service failed"},
		{"empty code", &plugin.RemoteError{Code: "", Message: "x"}, "operation_failed", "host service failed"},
		{"over-long message", &plugin.RemoteError{Code: "policy", Message: strings.Repeat("a", 5000)}, "operation_failed", "host service failed"},
		{"negative retry hint", &plugin.RemoteError{Code: "policy", Message: "x", RetryAfterMilliseconds: -1}, "operation_failed", "host service failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host := allow(func(context.Context, plugin.Request) (json.RawMessage, error) { return nil, c.remote })
			var seen *plugin.RemoteError
			r := open(t, context.Background(), reverseDescriptor(), func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
				_, err := plugin.ReverseFrom(ctx).Call(ctx, svc, "sign", nil)
				if !errors.As(err, &seen) {
					return nil, errors.New("the guest did not receive a RemoteError")
				}
				return json.RawMessage(`1`), nil
			}, host)
			defer r.stop()
			for i := 0; i < 2; i++ { // the second call proves the connection survived
				if _, err := r.session.Call(context.Background(), main1, "run", nil); err != nil {
					t.Fatalf("call %d: %v (state %s)", i+1, err, r.session.State())
				}
			}
			if seen == nil || seen.Code != c.wantCode || seen.Message != c.wantText {
				t.Fatalf("guest saw %+v, want code %q message %q", seen, c.wantCode, c.wantText)
			}
		})
	}
}

// The same holds for a guest handler that returns a RemoteError the host would
// reject: the call fails with operation_failed and the connection stays up.
func TestForwardHandlerRemoteErrors(t *testing.T) {
	for name, remote := range map[string]*plugin.RemoteError{
		"uppercase code":    {Code: "Bad", Message: "x"},
		"over-long message": {Code: "policy", Message: strings.Repeat("a", 5000)},
	} {
		t.Run(name, func(t *testing.T) {
			r := open(t, context.Background(), reverseDescriptor(), func(context.Context, plugin.Request) (json.RawMessage, error) { return nil, remote }, nil)
			defer r.stop()
			for i := 0; i < 2; i++ {
				_, err := r.session.Call(context.Background(), main1, "run", nil)
				if remoteCode(err) != "operation_failed" {
					t.Fatalf("call %d: %v", i+1, err)
				}
				if r.session.State() != plugin.StateReady {
					t.Fatalf("call %d: the session ended (%s)", i+1, r.session.State())
				}
			}
		})
	}
}
