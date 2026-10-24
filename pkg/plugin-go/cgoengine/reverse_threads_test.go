//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// These scenarios need a plugin that speaks when no call is in flight, and
// several requests at once: the C engine reads on a thread of its own and runs
// each request on another. The pure-Go session is the reference.

type openIn func(*testing.T, context.Context, string, *plugin.ReverseOptions) (reverseSession, error)

type concurrentOutcome struct {
	Collected  string
	Result     string
	Authorized []string
	Handled    []string
	Facts      []string
}

func snapshot(rec *recorder) ([]string, []string, []string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]string(nil), rec.Authorized...), append([]string(nil), rec.Handled...), append([]string(nil), rec.Facts...)
}

func callOp(t *testing.T, s reverseSession, op string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := s.Call(ctx, reverseRef, op, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	return string(result)
}

// A request made when no call is in flight is served in a context derived from
// the one the session was opened with, and has no parent.
func TestSessionScopedReverseMatchesGo(t *testing.T) {
	run := func(open openIn) concurrentOutcome {
		rec := &recorder{}
		ctx := context.WithValue(context.Background(), ctxKey, "open")
		s, err := open(t, ctx, "rev-go", rec.options(time.Second, 0))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Abort()
		rec.callDeadline = time.Now().Add(time.Minute)
		callOp(t, s, "idle")
		if !eventually(func() bool { return rec.count() >= 1 }) {
			t.Fatal("the host never saw the request")
		}
		out := concurrentOutcome{Collected: callOp(t, s, "collect")}
		out.Authorized, out.Handled, out.Facts = snapshot(rec)
		return out
	}
	for _, p := range openPairs() {
		t.Run(p.name, func(t *testing.T) {
			want, got := run(p.want), run(p.got)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("engine and Go session disagree\n  C:  %+v\n  Go: %+v", got, want)
			}
			// A plugin in the host's own process names its request itself.
			id := "g1"
			if !p.process {
				id = "r1"
			}
			if want.Collected != `{"result":"signed"}` || len(want.Handled) != 1 ||
				want.Handled[0] != `{"apiVersion":"ext.plugin/v1","id":"`+id+`","plugin":{"id":"cgoengine/reverse","revision":"r1"},"contract":{"name":"host.svc","version":"v1"},"operation":"sign","surface":"signing","deadline":"0001-01-01T00:00:00Z","payload":{"n":1}} deadline-set=true` ||
				want.Facts[0] != "deadline-within-call=true value=open" {
				t.Errorf("the reference answered %+v", want)
			}
		})
	}
}

// More requests than the host allows in flight: the others are refused as busy.
func TestReverseInFlightBoundMatchesGo(t *testing.T) {
	run := func(open openIn) string {
		rec := &recorder{release: make(chan struct{})}
		s, err := open(t, context.Background(), "rev-go", rec.options(5*time.Second, 0))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Abort()
		rec.callDeadline = time.Now().Add(time.Minute)
		go func() {
			if eventually(func() bool { return rec.count() >= 4 }) {
				time.Sleep(400 * time.Millisecond) // the other two requests arrive and are refused
			}
			close(rec.release)
		}()
		return callOp(t, s, "burst")
	}
	for _, p := range openPairs() {
		t.Run(p.name, func(t *testing.T) {
			want, got := run(p.want), run(p.got)
			if got != want {
				t.Errorf("engine %s, Go session %s", got, want)
			}
			if want != `{"busy":2,"ok":4}` {
				t.Errorf("the reference answered %s", want)
			}
		})
	}
}

// A request still running when its call ends is cancelled, and its answer is
// refused by the guest as a failure; one made on the session is cancelled when
// the session closes.
func TestReverseCancellationMatchesGo(t *testing.T) {
	type outcome struct {
		LingerFacts, CloseFacts []string
		Collected               string
	}
	run := func(open openIn) outcome {
		var out outcome
		{
			rec := &recorder{}
			s, err := open(t, context.Background(), "rev-go", rec.options(5*time.Second, 0))
			if err != nil {
				t.Fatal(err)
			}
			rec.callDeadline = time.Now().Add(time.Minute)
			callOp(t, s, "linger")
			if !eventually(func() bool { return rec.hasFact("canceled=true") }) {
				t.Error("the request was not cancelled when its call ended")
			}
			out.Collected = callOp(t, s, "collect")
			_, _, out.LingerFacts = snapshot(rec)
			s.Abort()
		}
		{
			rec := &recorder{}
			s, err := open(t, context.Background(), "rev-go", rec.options(5*time.Second, 0))
			if err != nil {
				t.Fatal(err)
			}
			rec.callDeadline = time.Now().Add(time.Minute)
			callOp(t, s, "sessionhang")
			if !eventually(func() bool { return rec.count() >= 1 }) {
				t.Fatal("the host never saw the session request")
			}
			if rec.hasFact("canceled=true") {
				t.Error("cancelled before the session closed")
			}
			s.Abort()
			if !eventually(func() bool { return rec.hasFact("canceled=true") }) {
				t.Error("the session request was not cancelled when the session closed")
			}
			_, _, out.CloseFacts = snapshot(rec)
		}
		return out
	}
	for _, p := range openPairs() {
		t.Run(p.name, func(t *testing.T) {
			want, got := run(p.want), run(p.got)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("engine and Go session disagree\n  C:  %+v\n  Go: %+v", got, want)
			}
			if want.Collected != `{"error":{"code":"operation_failed"},"result":null}` {
				t.Errorf("the reference answered %+v", want)
			}
		})
	}
}

// What a plugin does between calls ends the session, and the next call fails.
// A frame that breaks the rules is acted on at once: the engine stops the plugin
// and the next call finds the host closed. A plugin that merely leaves may still
// be exiting (a guest built with the race detector sleeps for a second at exit),
// and a call made before the exit lands fails with the lost connection instead;
// the Go client behaves the same way. The C test pins the closed case. So the
// comparison asks of both engines that the call and the session fail.
func TestIdleBehaviorMatchesGo(t *testing.T) {
	for _, c := range []struct {
		mode     string
		wantC    []string
		explains string
	}{
		{"rev-raw:idle-bad", []string{"closed,"}, "stopped by the engine"},
		{"rev-raw:idle-exit", []string{"closed,", "other"}, "a plugin that left"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			run := func(open openIn) [2]string {
				rec := &recorder{}
				s, err := open(t, context.Background(), c.mode, rec.options(time.Second, 0))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Abort()
				time.Sleep(400 * time.Millisecond) // the plugin has spoken, or left
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_, err = s.Call(ctx, reverseRef, "plain", nil)
				return [2]string{causes(err), string(s.State())}
			}
			for _, p := range processPairs() {
				want, got := run(p.want), run(p.got)
				if want[0] == "ok" || want[1] != string(plugin.StateFailed) {
					t.Errorf("%s: the reference answered %v: the call and the session should fail", p.name, want)
				}
				// The engine owns the child in one pair and reads it itself, so its
				// class is pinned; over a backend the client reports what it saw and
				// the call and the session simply fail.
				known := got[0] != "ok" && p.name != "engine-owned process"
				for _, class := range c.wantC {
					known = known || got[0] == class
				}
				if !known || got[1] != string(plugin.StateFailed) {
					t.Errorf("%s: engine %v for %s, want one of %v and the failed state", p.name, got, c.explains, c.wantC)
				}
			}
		})
	}
}
