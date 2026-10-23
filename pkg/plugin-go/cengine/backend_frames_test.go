//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/process"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/inprocess"
)

// A session over a Go backend (Open with Connect) carries progress events and
// reverse calls too: the backend hands the engine the frames, and the engine
// decides as it does for a process it reads. These tests run the same plugins
// through the pure-Go session and through the engine over the same kind of
// backend, and compare what the host and the plugin can observe.

// openPair is a reference and the engine over the same kind of backend.
type openPair struct {
	name      string
	want, got openIn
	// process is true when the plugin is a child that can be told to misbehave.
	process bool
}

func openPairs() []openPair {
	return []openPair{
		{"engine-owned process", openGoReverseIn, openCReverseIn, true},
		{"JSON-line backend", openGoReverseIn, openCBackendReverseIn, true},
		{"in-process backend", openGoInProcessReverseIn, openCInProcessReverseIn, false},
	}
}

func processPairs() []openPair {
	var out []openPair
	for _, p := range openPairs() {
		if p.process {
			out = append(out, p)
		}
	}
	return out
}

// openCBackendReverseIn opens the guest as a child process the Go JSON-line
// client talks to, with the C engine owning the session.
func openCBackendReverseIn(t *testing.T, ctx context.Context, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	t.Helper()
	exe := guestPath(t)
	options := reverseOpts()
	options.Reverse = reverse
	options.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: exe, Env: []string{guestEnv + "=" + mode}})
	}
	s, err := Open(ctx, reverseDescriptor(), options)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func openCBackendReverse(t *testing.T, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	return openCBackendReverseIn(t, context.Background(), mode, reverse)
}

// The in-process backends run the Go reverse guest in the test's own process.
func openGoInProcessReverseIn(t *testing.T, ctx context.Context, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	t.Helper()
	options := inProcessOptions(t, reverse)
	s, err := plugin.Open(ctx, reverseDescriptor(), options)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func openCInProcessReverseIn(t *testing.T, ctx context.Context, mode string, reverse *plugin.ReverseOptions) (reverseSession, error) {
	t.Helper()
	options := inProcessOptions(t, reverse)
	s, err := Open(ctx, reverseDescriptor(), options)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func inProcessOptions(t *testing.T, reverse *plugin.ReverseOptions) plugin.Options {
	t.Helper()
	options := reverseOpts()
	options.Reverse = reverse
	options.Connect = func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
		guest, err := newReverseGuest()
		if err != nil {
			return nil, err
		}
		return inprocess.New(guest)
	}
	return options
}

func openCBackend(t *testing.T, mode string) (progressSession, error) {
	t.Helper()
	exe := guestPath(t)
	options := progressOptions()
	options.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: exe, Env: []string{guestEnv + "=" + mode}})
	}
	s, err := Open(context.Background(), progressDescriptor(), options)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func openGoInProcess(t *testing.T, mode string) (progressSession, error) {
	t.Helper()
	s, err := plugin.Open(context.Background(), progressDescriptor(), inProcessProgress())
	if err != nil {
		return nil, err
	}
	return s, nil
}

func openCInProcess(t *testing.T, mode string) (progressSession, error) {
	t.Helper()
	s, err := Open(context.Background(), progressDescriptor(), inProcessProgress())
	if err != nil {
		return nil, err
	}
	return s, nil
}

func inProcessProgress() plugin.Options {
	options := progressOptions()
	options.Connect = func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
		guest, err := newProgressGuest()
		if err != nil {
			return nil, err
		}
		return inprocess.New(guest)
	}
	return options
}

// A plugin whose descriptor declares progress or reverse operations opens over
// any backend, and a host may offer services to it.
func TestBackendOpensPluginsThatUseFrames(t *testing.T) {
	d := testDescriptor()
	d.Reverse = []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "host.svc", Version: "v1"}, Operations: []plugin.Operation{{Name: "sign"}}}}
	d.Contracts[0].Operations[0].Progress = true
	options := referenceOptions(&testBackend{d: d})
	options.Reverse = (&recorder{}).options(0, 0)
	s, err := Open(context.Background(), d, options)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Abort(); err != nil {
		t.Fatal(err)
	}
}

// Progress events over a backend, in the child process and in the host's own.
func TestBackendProgressMatchesGo(t *testing.T) {
	for _, p := range []struct {
		name      string
		want, got func(*testing.T, string) (progressSession, error)
	}{
		{"JSON-line backend", openGo, openCBackend},
		{"in-process backend", openGoInProcess, openCInProcess},
	} {
		t.Run(p.name, func(t *testing.T) {
			compare := func(op string, options ...plugin.CallOptions) progressOutcome {
				t.Helper()
				want := progressRun(t, p.want, "go", op, options...)
				got := progressRun(t, p.got, "go", op, options...)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: engine and Go session disagree\n  engine: %+v\n  Go:     %+v", op, got, want)
				}
				return want
			}
			if got := compare("stream"); len(got.Events) != 3 || got.Err != "ok" {
				t.Fatalf("stream: %+v", got)
			}
			if got := compare("plain"); len(got.Events) != 0 || got.Err != "ok" {
				t.Fatalf("plain: %+v", got)
			}
			if got := compare("fail"); len(got.Events) != 1 || got.Err != "remote:busy," {
				t.Fatalf("fail: %+v", got)
			}
			low := plugin.CallOptions{Progress: plugin.ProgressLimits{EventsPerSecond: 5}}
			got := compare("flood", low)
			var delivered, lost uint64
			for _, e := range got.Events {
				if e.Text != "" {
					delivered++
				}
				lost += e.Dropped
			}
			if delivered < 10 || delivered+lost != 300 {
				t.Fatalf("flood: %d delivered and %d lost of 300", delivered, lost)
			}
			for _, c := range []struct {
				op      string
				options []plugin.CallOptions
				want    string
			}{
				{"limits", nil, `{"bytes":65536,"declared":true,"events":50}`},
				{"limits", []plugin.CallOptions{{Progress: plugin.ProgressLimits{EventsPerSecond: 7, BytesPerSecond: 900}}}, `{"bytes":900,"declared":true,"events":7}`},
				{"plainlimits", nil, `{"bytes":0,"declared":false,"events":0}`},
			} {
				if got := compare(c.op, c.options...); got.Result != c.want {
					t.Errorf("%s: result %s, want %s", c.op, got.Result, c.want)
				}
			}
		})
	}
}

// A plugin in the host's own process is told when the rate limit dropped one of
// its events: Report returns plugin.ErrProgressDropped. The engine decides that,
// so the count the plugin saw plus the events the host received is what it sent.
func TestInProcessProgressReportsDrops(t *testing.T) {
	low := plugin.CallOptions{Progress: plugin.ProgressLimits{EventsPerSecond: 5}}
	for name, open := range map[string]func(*testing.T, string) (progressSession, error){"Go session": openGoInProcess, "engine": openCInProcess} {
		out := progressRun(t, open, "go", "flooddrops", low)
		if out.Err != "ok" {
			t.Fatalf("%s: %+v", name, out)
		}
		var result struct{ Drops int }
		if err := json.Unmarshal([]byte(out.Result), &result); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		delivered := 0
		for _, e := range out.Events {
			if e.Text != "" {
				delivered++
			}
		}
		if result.Drops == 0 || result.Drops+delivered != 300 {
			t.Errorf("%s: the plugin was told of %d drops and the host got %d of 300", name, result.Drops, delivered)
		}
	}
}

// A plugin that breaks the rules, through a backend: the same class of error and
// the session does not survive it.
func TestBackendProgressViolationsMatchGo(t *testing.T) {
	for _, c := range []struct{ mode, op, want string }{
		{"undeclared", "plain", "protocol,"},
		{"wrongparent", "stream", "protocol,"},
		{"badkind", "stream", "protocol,"},
		{"streamprogress", "stream", "protocol,"},
		{"overtotal", "stream", "protocol,"},
		{"bigtext", "stream", "protocol,"},
		{"unknown", "stream", "protocol,"},
		{"reverseresponse", "stream", "protocol,"},
		{"hellofirst", "stream", "protocol,"},
		{"wrongid", "stream", "protocol,"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			want := progressRun(t, openGo, "raw:"+c.mode, c.op)
			got := progressRun(t, openCBackend, "raw:"+c.mode, c.op)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("engine and Go session disagree\n  engine: %+v\n  Go:     %+v", got, want)
			}
			if want.Err != c.want || got.State == plugin.StateReady {
				t.Errorf("Go answered %q (want %q); engine state %s", want.Err, c.want, got.State)
			}
		})
	}
}

// The plugin's own context ends while the host is serving its request, which ends
// the host's handler, as in the pure-Go session.
func TestInProcessReverseEndsWithThePluginsContext(t *testing.T) {
	run := func(open openIn) ([]string, string) {
		rec := &recorder{}
		s, err := open(t, context.Background(), "rev-go", rec.options(5*time.Second, 0))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Abort()
		rec.callDeadline = time.Now().Add(time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		started := time.Now()
		result, err := s.Call(ctx, reverseRef, "call", json.RawMessage(`{"op":"hang","cancelMs":60}`))
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Errorf("the handler ran on for %v", elapsed)
		}
		_, _, facts := snapshot(rec)
		return facts, string(result)
	}
	for _, p := range openPairs() {
		if p.process {
			continue
		}
		wantFacts, wantResult := run(p.want)
		gotFacts, gotResult := run(p.got)
		if !reflect.DeepEqual(gotFacts, wantFacts) || gotResult != wantResult {
			t.Errorf("%s: engine %v %s, Go session %v %s", p.name, gotFacts, gotResult, wantFacts, wantResult)
		}
		if len(wantFacts) == 0 || wantFacts[len(wantFacts)-1] != "canceled=true" {
			t.Errorf("%s: the reference did not cancel the handler: %v", p.name, wantFacts)
		}
	}
}

// A request made after the session is closed is refused, never served.
func TestBackendReverseAfterCloseIsRefused(t *testing.T) {
	rec := &recorder{}
	d := reverseDescriptor()
	var held plugin.ReverseCaller
	options := reverseOpts()
	options.Reverse = rec.options(time.Second, 0)
	options.Connect = func(context.Context, plugin.Descriptor) (plugin.Backend, error) {
		guest, err := plugin.NewGuest(d, plugin.GuestOptions{Handler: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
			held = plugin.SessionReverseFrom(ctx)
			return json.RawMessage(`{}`), nil
		}})
		if err != nil {
			return nil, err
		}
		return inprocess.New(guest)
	}
	s, err := Open(context.Background(), d, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(context.Background(), reverseRef, "plain", nil); err != nil {
		t.Fatal(err)
	}
	if held == nil {
		t.Fatal("the plugin was given no session caller")
	}
	if _, err := held.Call(context.Background(), hostRef, "sign", nil); err != nil {
		t.Fatalf("a request while the session is open: %v", err)
	}
	if err := s.Abort(); err != nil {
		t.Fatal(err)
	}
	_, err = held.Call(context.Background(), hostRef, "sign", nil)
	var remote *plugin.RemoteError
	if !errors.As(err, &remote) || remote.Code != plugin.ReverseUnavailable {
		t.Fatalf("after close: %v", err)
	}
}
