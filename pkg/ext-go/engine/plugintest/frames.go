package plugintest

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
)

// The frames conformance suite: progress events and reverse calls, as a language
// SDK's guest must produce them. FramesGuest is the reference guest, FramesDescriptor
// its descriptor (lib/plugin/testdata/v1/frames-descriptor.json holds the same JSON
// for the other languages), and docs/plugin-guest-frames.md says what every
// operation does. RunFrames drives a guest through a real transport with services
// that record what the host saw, and checks what the host and the guest observed.

var (
	FramesContract = plugin.ContractRef{Name: "ext.frames", Version: "v1"}
	FramesHostRef  = plugin.ContractRef{Name: "host.svc", Version: "v1"}
)

func FramesDescriptor() plugin.Descriptor {
	ops := func(progress bool, names ...string) []plugin.Operation {
		var out []plugin.Operation
		for _, n := range names {
			op := plugin.Operation{Name: n, Progress: progress}
			if n == "sign" {
				op.Surface = "signing"
			}
			out = append(out, op)
		}
		return out
	}
	return plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "ctx/frames", Revision: "fixture-1"},
		Contracts: []plugin.Contract{{ContractRef: FramesContract, Operations: append(
			ops(true, "stream", "flood", "limits", "split"),
			ops(false, "plain", "call", "twice", "idle", "collect", "burst")...)}},
		Reverse: []plugin.Contract{{ContractRef: FramesHostRef, Operations: ops(false, "sign", "secret", "empty", "fail", "errok", "errretryok", "big", "hold")}},
	}
}

// outcome is how an operation reports what a reverse call came to.
func framesOutcome(result json.RawMessage, err error) map[string]any {
	report := map[string]any{}
	if err == nil {
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		report["result"] = result
		return report
	}
	var remote *plugin.RemoteError
	switch {
	case errors.As(err, &remote):
		e := map[string]any{"code": remote.Code, "message": remote.Message}
		if remote.RetryAfterMilliseconds != 0 {
			e["retry"] = remote.RetryAfterMilliseconds
		}
		report["error"] = e
	case errors.Is(err, plugin.ErrClosed):
		report["error"] = map[string]any{"other": "closed"}
	case errors.Is(err, context.DeadlineExceeded):
		report["error"] = map[string]any{"other": "deadline"}
	case errors.Is(err, context.Canceled):
		report["error"] = map[string]any{"other": "canceled"}
	default:
		report["error"] = map[string]any{"other": "failed"}
	}
	return report
}

// FramesGuest is the reference guest of the frames suite.
func FramesGuest() (*plugin.Guest, error) {
	background := make(chan json.RawMessage, 16) // outcomes of session-scoped calls, for "collect"
	return plugin.NewGuest(FramesDescriptor(), plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
		report := plugin.ProgressFrom(ctx)
		reply := func(v any) (json.RawMessage, error) { return json.Marshal(v) }
		switch r.Operation {
		case "stream":
			_ = report.Report(plugin.Progress{Kind: "progress", Completed: 1, Total: 3})
			_ = report.Report(plugin.Progress{Kind: "log", Stream: "out", Text: "hello"})
			_ = report.Report(plugin.Progress{Kind: "log", Stream: "err", Text: "oops"})
			return reply(map[string]any{"done": true})
		case "flood":
			var in struct{ Count int }
			in.Count = 300
			if len(r.Payload) != 0 {
				_ = json.Unmarshal(r.Payload, &in)
			}
			dropped := 0
			for i := 0; i < in.Count; i++ {
				if errors.Is(report.Report(plugin.Progress{Kind: "log", Text: "x"}), plugin.ErrProgressDropped) {
					dropped++
				}
			}
			return reply(map[string]any{"dropped": dropped})
		case "limits":
			l, ok := plugin.ProgressLimitsFrom(ctx)
			return reply(map[string]any{"declared": ok, "events": l.EventsPerSecond, "bytes": l.BytesPerSecond})
		case "split":
			var in struct{ Text string }
			if err := json.Unmarshal(r.Payload, &in); err != nil {
				return nil, err
			}
			pieces := plugin.SplitProgressText(in.Text)
			for _, p := range pieces {
				_ = report.Report(plugin.Progress{Kind: "log", Stream: "out", Text: p})
			}
			return reply(map[string]any{"pieces": len(pieces)})
		case "plain":
			err := report.Report(plugin.Progress{Kind: "log", Text: "never sent"})
			_, ok := plugin.ProgressLimitsFrom(ctx)
			return reply(map[string]any{"unsupported": errors.Is(err, plugin.ErrUnsupported), "limits": ok})
		case "call":
			var in struct {
				Op      string
				Payload json.RawMessage
			}
			if err := json.Unmarshal(r.Payload, &in); err != nil {
				return nil, err
			}
			caller := plugin.ReverseFrom(ctx)
			if caller == nil {
				return reply(map[string]any{"error": map[string]any{"other": "unsupported"}})
			}
			return reply(framesOutcome(caller.Call(ctx, FramesHostRef, in.Op, in.Payload)))
		case "twice":
			caller := plugin.ReverseFrom(ctx)
			first, err := caller.Call(ctx, FramesHostRef, "sign", json.RawMessage("1"))
			if err != nil {
				return nil, err
			}
			second, err := caller.Call(ctx, FramesHostRef, "sign", json.RawMessage("2"))
			if err != nil {
				return nil, err
			}
			return reply(map[string]any{"first": json.RawMessage(first), "second": json.RawMessage(second)})
		case "idle":
			session := plugin.SessionReverseFrom(ctx)
			go func() {
				time.Sleep(20 * time.Millisecond)
				callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				raw, _ := json.Marshal(framesOutcome(session.Call(callCtx, FramesHostRef, "sign", json.RawMessage(`{"n":1}`))))
				background <- raw
			}()
			return reply(map[string]any{"started": true})
		case "collect":
			select {
			case raw := <-background:
				return raw, nil
			case <-time.After(5 * time.Second):
				return reply(map[string]any{"timeout": true})
			}
		case "burst":
			caller := plugin.ReverseFrom(ctx)
			var wg sync.WaitGroup
			var mu sync.Mutex
			counts := map[string]int{}
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := caller.Call(ctx, FramesHostRef, "hold", nil)
					key := "ok"
					var remote *plugin.RemoteError
					if errors.As(err, &remote) {
						key = remote.Code
					} else if err != nil {
						key = "other"
					}
					mu.Lock()
					counts[key]++
					mu.Unlock()
				}()
			}
			wg.Wait()
			return reply(counts)
		}
		return nil, errors.New("unknown operation")
	}})
}

// FramesHost is the session surface the frames suite uses.
type FramesHost interface {
	Call(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)
	CallProgress(context.Context, plugin.ContractRef, string, json.RawMessage, func(plugin.Progress), ...plugin.CallOptions) (json.RawMessage, error)
	State() plugin.State
	Abort() error
}
type OpenFramesHost func(context.Context, plugin.Descriptor, plugin.Options) (FramesHost, error)

// FramesOptions says what the guest under test can do.
type FramesOptions struct {
	// Concurrent is true for a guest that can wait for the host's answer to a
	// request and still serve what arrives, so it can have several requests
	// outstanding at once and call its host between forward calls (a thread, or an
	// event loop). A guest without it, such as a WebAssembly command or a
	// single-threaded one, answers those operations with the public error
	// "unsupported".
	Concurrent bool
}

// services records what the host's policy and handler were shown.
type services struct {
	mu                  sync.Mutex
	authorized, handled []plugin.Request
	release             chan struct{} // closed to let "hold" return
}

func (s *services) counts() (authorized, handled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.authorized), len(s.handled)
}

func (s *services) options() *plugin.ReverseOptions {
	return &plugin.ReverseOptions{
		Timeout:         5 * time.Second,
		MaxPayloadBytes: 64,
		Authorize: func(_ context.Context, r plugin.Request) error {
			s.mu.Lock()
			s.authorized = append(s.authorized, r)
			s.mu.Unlock()
			if r.Operation == "secret" {
				return errors.New("not for this plugin")
			}
			return nil
		},
		Handle: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
			s.mu.Lock()
			s.handled = append(s.handled, r)
			s.mu.Unlock()
			switch r.Operation {
			case "sign":
				return json.RawMessage(`"signed"`), nil
			case "empty":
				return nil, nil
			case "fail":
				return nil, errors.New("boom")
			case "errok":
				return nil, &plugin.RemoteError{Code: "policy", Message: "refused for you", RetryAfterMilliseconds: 5}
			case "errretryok":
				return nil, &plugin.RemoteError{Code: "policy", Message: "x", RetryAfterMilliseconds: 31536000000}
			case "big":
				return json.RawMessage(`"` + strings.Repeat("0123456789", 7) + `"`), nil
			case "hold":
				select {
				case <-s.release:
					return json.RawMessage(`"held"`), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, errors.New("unknown operation")
		},
	}
}

func eventually(cond func() bool) bool {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// RunFrames runs the frames suite against guests the pure-Go session hosts.
func RunFrames(t *testing.T, factory Factory, opts FramesOptions) {
	RunFramesWithHost(t, factory, func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (FramesHost, error) {
		return plugin.Open(ctx, d, o)
	}, opts)
}

func RunFramesWithHost(t *testing.T, factory Factory, openHost OpenFramesHost, opts FramesOptions) {
	t.Helper()
	type opened struct {
		host    FramesHost
		seen    *services
		forward func() []plugin.Request
	}
	open := func(t *testing.T, offer bool) opened {
		t.Helper()
		seen := &services{release: make(chan struct{})}
		var mu sync.Mutex
		var forward []plugin.Request
		options := plugin.Options{
			Verify: func(context.Context, plugin.Descriptor) error { return nil },
			Authorize: func(_ context.Context, r plugin.Request) error {
				mu.Lock()
				forward = append(forward, r)
				mu.Unlock()
				return nil
			},
			Connect: func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) { return factory(ctx) },
			Timeout: 10 * time.Second,
		}
		if offer {
			options.Reverse = seen.options()
		}
		h, err := openHost(context.Background(), FramesDescriptor(), options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Abort() })
		return opened{h, seen, func() []plugin.Request {
			mu.Lock()
			defer mu.Unlock()
			return append([]plugin.Request(nil), forward...)
		}}
	}
	callCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 20*time.Second)
	}
	// run calls an operation, with progress events, and decodes its result.
	run := func(t *testing.T, h FramesHost, op string, payload string, events *[]plugin.Progress, options ...plugin.CallOptions) map[string]json.RawMessage {
		t.Helper()
		ctx, cancel := callCtx()
		defer cancel()
		var raw json.RawMessage
		var err error
		if events != nil {
			raw, err = h.CallProgress(ctx, FramesContract, op, json.RawMessage(payload), func(p plugin.Progress) { *events = append(*events, p) }, options...)
		} else {
			raw, err = h.Call(ctx, FramesContract, op, json.RawMessage(payload))
		}
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s: result %s: %v", op, raw, err)
		}
		return out
	}
	text := func(v json.RawMessage) string { return string(v) }
	// outcomeOf runs the "call" operation and returns the reverse call's outcome.
	outcomeOf := func(t *testing.T, o opened, op, payload string) (result, code, message string, retry int64) {
		t.Helper()
		in, _ := json.Marshal(map[string]any{"op": op, "payload": json.RawMessage(payload)})
		out := run(t, o.host, "call", string(in), nil)
		if e, ok := out["error"]; ok {
			var f struct {
				Code, Message, Other string
				Retry                int64
			}
			if err := json.Unmarshal(e, &f); err != nil {
				t.Fatal(err)
			}
			if f.Other != "" {
				return "", "other:" + f.Other, "", 0
			}
			return "", f.Code, f.Message, f.Retry
		}
		return text(out["result"]), "", "", 0
	}

	t.Run("progress-events-in-order", func(t *testing.T) {
		o := open(t, true)
		var events []plugin.Progress
		out := run(t, o.host, "stream", "", &events)
		want := []plugin.Progress{{Kind: "progress", Completed: 1, Total: 3}, {Kind: "log", Stream: "out", Text: "hello"}, {Kind: "log", Stream: "err", Text: "oops"}}
		if !reflect.DeepEqual(events, want) || text(out["done"]) != "true" {
			t.Fatalf("events %+v, result %v", events, out)
		}
		// A host that asks for no events gets the same result.
		if out := run(t, o.host, "stream", "", nil); text(out["done"]) != "true" {
			t.Fatalf("plain call: %v", out)
		}
		if o.host.State() != plugin.StateReady {
			t.Fatal("the session did not survive")
		}
	})
	t.Run("progress-flood-is-limited-and-counted", func(t *testing.T) {
		o := open(t, true)
		var events []plugin.Progress
		out := run(t, o.host, "flood", `{"count":300}`, &events, plugin.CallOptions{Progress: plugin.ProgressLimits{EventsPerSecond: 5}})
		var dropped int
		if err := json.Unmarshal(out["dropped"], &dropped); err != nil {
			t.Fatal(err)
		}
		var delivered, reported uint64
		for _, e := range events {
			if e.Text != "" {
				delivered++
			}
			reported += e.Dropped
		}
		// The guest was told of each drop, the host got the rest, and every drop
		// was reported in an event or in the marker before the response.
		if delivered < 10 || delivered > 60 || int(delivered)+dropped != 300 || int(reported) != dropped {
			t.Fatalf("%d delivered, %d dropped (told to the guest), %d reported of 300", delivered, dropped, reported)
		}
	})
	t.Run("progress-limits-reach-the-guest", func(t *testing.T) {
		o := open(t, true)
		for _, c := range []struct {
			options []plugin.CallOptions
			want    string
		}{
			{nil, `{"bytes":65536,"declared":true,"events":50}`},
			{[]plugin.CallOptions{{Progress: plugin.ProgressLimits{EventsPerSecond: 7, BytesPerSecond: 900}}}, `{"bytes":900,"declared":true,"events":7}`},
			{[]plugin.CallOptions{{Progress: plugin.ProgressLimits{EventsPerSecond: 7}}}, `{"bytes":65536,"declared":true,"events":7}`},
		} {
			var events []plugin.Progress
			out := run(t, o.host, "limits", "", &events, c.options...)
			got, _ := json.Marshal(out)
			if string(got) != c.want {
				t.Errorf("limits %v: %s, want %s", c.options, got, c.want)
			}
		}
	})
	t.Run("progress-undeclared-operation", func(t *testing.T) {
		o := open(t, true)
		var events []plugin.Progress
		out := run(t, o.host, "plain", "", &events)
		if text(out["unsupported"]) != "true" || text(out["limits"]) != "false" || len(events) != 0 {
			t.Fatalf("result %v, events %v", out, events)
		}
	})
	t.Run("progress-text-is-split-on-characters", func(t *testing.T) {
		o := open(t, true)
		long := strings.Repeat("a", plugin.MaxProgressTextBytes-1) + "€" + strings.Repeat("b", 10) + strings.Repeat("é", 2000)
		in, _ := json.Marshal(map[string]string{"text": long})
		var events []plugin.Progress
		out := run(t, o.host, "split", string(in), &events)
		var joined strings.Builder
		for _, e := range events {
			if len(e.Text) > plugin.MaxProgressTextBytes || e.Stream != "out" || e.Kind != "log" {
				t.Fatalf("event %d bytes, %+v", len(e.Text), e)
			}
			joined.WriteString(e.Text)
		}
		if joined.String() != long || text(out["pieces"]) != fmt.Sprint(len(events)) || len(events) < 3 {
			t.Fatalf("%d events reassemble to %d of %d bytes; result %v", len(events), joined.Len(), len(long), out)
		}
	})

	t.Run("reverse-answers", func(t *testing.T) {
		o := open(t, true)
		type got struct {
			result, code, message string
			retry                 int64
		}
		for _, c := range []struct {
			op, payload string
			want        got
		}{
			{"sign", `{"n":1}`, got{result: `"signed"`}},
			{"empty", `null`, got{result: `null`}},
			{"secret", `null`, got{code: "denied", message: "reverse call refused"}},
			{"fail", `null`, got{code: "operation_failed", message: "host service failed"}},
			{"errok", `null`, got{code: "policy", message: "refused for you", retry: 5}},
			{"errretryok", `null`, got{code: "policy", message: "x", retry: 31536000000}},
			{"big", `null`, got{code: "too_large", message: "reverse call refused"}},
		} {
			result, code, message, retry := outcomeOf(t, o, c.op, c.payload)
			if (got{result, code, message, retry}) != c.want {
				t.Errorf("%s: %q %q %q %d, want %+v", c.op, result, code, message, retry, c.want)
			}
		}
		if o.host.State() != plugin.StateReady {
			t.Fatal("a refusal ended the session")
		}
	})
	t.Run("reverse-undeclared-is-refused-by-the-guest", func(t *testing.T) {
		o := open(t, true)
		_, code, _, _ := outcomeOf(t, o, "nothere", `null`)
		authorized, handled := o.seen.counts()
		if code != plugin.ReverseUndeclared || authorized != 0 || handled != 0 {
			t.Fatalf("code %q, authorized %d, handled %d", code, authorized, handled)
		}
	})
	t.Run("reverse-request-as-the-host-saw-it", func(t *testing.T) {
		o := open(t, true)
		outcomeOf(t, o, "sign", `{"n":1}`)
		o.seen.mu.Lock()
		defer o.seen.mu.Unlock()
		if len(o.seen.authorized) != 1 || len(o.seen.handled) != 1 {
			t.Fatalf("authorized %d, handled %d", len(o.seen.authorized), len(o.seen.handled))
		}
		r := o.seen.handled[0]
		forward := o.forward()
		if len(forward) != 1 {
			t.Fatalf("forward calls %d", len(forward))
		}
		if r.ID != "g1" || r.Parent != forward[0].ID || r.Plugin != FramesDescriptor().Identity || r.Contract != FramesHostRef ||
			r.Operation != "sign" || r.Surface != "signing" || string(r.Payload) != `{"n":1}` || r.Deadline.IsZero() || r.Deadline.After(forward[0].Deadline) {
			t.Fatalf("request %+v, forward call %+v", r, forward[0])
		}
	})
	t.Run("reverse-calls-in-sequence", func(t *testing.T) {
		o := open(t, true)
		out := run(t, o.host, "twice", "", nil)
		if text(out["first"]) != `"signed"` || text(out["second"]) != `"signed"` {
			t.Fatalf("result %v", out)
		}
		o.seen.mu.Lock()
		defer o.seen.mu.Unlock()
		if len(o.seen.handled) != 2 || o.seen.handled[0].ID != "g1" || o.seen.handled[1].ID != "g2" || string(o.seen.handled[0].Payload) != "1" || string(o.seen.handled[1].Payload) != "2" {
			t.Fatalf("handled %+v", o.seen.handled)
		}
	})
	t.Run("reverse-without-services-is-unavailable", func(t *testing.T) {
		o := open(t, false)
		_, code, _, _ := outcomeOf(t, o, "sign", `null`)
		if code != plugin.ReverseUnavailable {
			t.Fatalf("code %q", code)
		}
		if o.host.State() != plugin.StateReady {
			t.Fatal("a refusal ended the session")
		}
	})
	if !opts.Concurrent {
		t.Run("concurrent-operations-are-unsupported", func(t *testing.T) {
			o := open(t, true)
			for _, op := range []string{"idle", "collect", "burst"} {
				ctx, cancel := callCtx()
				_, err := o.host.Call(ctx, FramesContract, op, nil)
				cancel()
				var remote *plugin.RemoteError
				if !errors.As(err, &remote) || remote.Code != "unsupported" {
					t.Errorf("%s: %v", op, err)
				}
			}
		})
		return
	}
	t.Run("reverse-session-scoped-call-after-the-call-returned", func(t *testing.T) {
		o := open(t, true)
		if out := run(t, o.host, "idle", "", nil); text(out["started"]) != "true" {
			t.Fatalf("idle: %v", out)
		}
		if !eventually(func() bool { _, h := o.seen.counts(); return h >= 1 }) {
			t.Fatal("the host never saw the session-scoped request")
		}
		out := run(t, o.host, "collect", "", nil)
		if text(out["result"]) != `"signed"` {
			t.Fatalf("collected %v", out)
		}
		o.seen.mu.Lock()
		defer o.seen.mu.Unlock()
		if r := o.seen.handled[0]; r.Parent != "" || r.Operation != "sign" || string(r.Payload) != `{"n":1}` {
			t.Fatalf("session-scoped request %+v", r)
		}
	})
	t.Run("reverse-requests-in-flight-are-bounded", func(t *testing.T) {
		o := open(t, true)
		go func() {
			if eventually(func() bool { _, h := o.seen.counts(); return h >= 4 }) {
				time.Sleep(400 * time.Millisecond) // the other two requests arrive and are refused
			}
			close(o.seen.release)
		}()
		out := run(t, o.host, "burst", "", nil)
		got, _ := json.Marshal(out)
		if string(got) != `{"busy":2,"ok":4}` {
			t.Fatalf("burst: %s", got)
		}
	})
}
