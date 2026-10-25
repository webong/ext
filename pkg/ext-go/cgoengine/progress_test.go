//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
)

// progressSession is the part of a session the progress comparison uses.
type progressSession interface {
	CallProgress(context.Context, plugin.ContractRef, string, json.RawMessage, func(plugin.Progress), ...plugin.CallOptions) (json.RawMessage, error)
	State() plugin.State
	Abort() error
}

func progressOptions() plugin.Options {
	return plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Timeout:   20 * time.Second,
	}
}

func guestPath(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// openGo opens the guest in the pure-Go session: the reference.
func openGo(t *testing.T, mode string) (progressSession, error) {
	t.Helper()
	exe := guestPath(t)
	options := progressOptions()
	options.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		return process.Start(ctx, process.Command{Path: exe, Env: []string{guestEnv + "=" + mode}})
	}
	s, err := plugin.Open(context.Background(), progressDescriptor(), options)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// openC opens the same guest in a session whose child the C engine owns.
func openC(t *testing.T, mode string) (progressSession, error) {
	t.Helper()
	launch := LaunchOptions{ProcessOptions: ProcessOptions{Executable: guestPath(t), Environment: []string{guestEnv + "=" + mode}}}
	s, err := OpenProcess(context.Background(), progressDescriptor(), launch, progressOptions())
	if err != nil {
		return nil, err
	}
	return s, nil
}

type progressOutcome struct {
	Events []plugin.Progress
	Result string
	Err    string
	State  plugin.State
}

// progressRun opens the guest in mode, makes one call of op and reports what a
// host can observe.
func progressRun(t *testing.T, open func(*testing.T, string) (progressSession, error), mode, op string, options ...plugin.CallOptions) progressOutcome {
	t.Helper()
	s, err := open(t, mode)
	if err != nil {
		return progressOutcome{Err: causes(err)}
	}
	defer s.Abort()
	var out progressOutcome
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := s.CallProgress(ctx, progressRef, op, nil, func(p plugin.Progress) { out.Events = append(out.Events, p) }, options...)
	out.Result, out.Err, out.State = string(result), causes(err), s.State()
	return out
}

func compareProgress(t *testing.T, mode, op string, options ...plugin.CallOptions) (progressOutcome, progressOutcome) {
	t.Helper()
	want := progressRun(t, openGo, mode, op, options...)
	got := progressRun(t, openC, mode, op, options...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s/%s: C engine and Go session disagree\n  C:  %+v\n  Go: %+v", mode, op, got, want)
	}
	return got, want
}

func TestProgressMatchesGo(t *testing.T) {
	// A guest that follows the rules: events in order, then the result.
	got, _ := compareProgress(t, "go", "stream")
	if len(got.Events) != 3 || got.Result != `{"done":true}` || got.Err != "ok" {
		t.Fatalf("stream: %+v", got)
	}
	// An operation that declared no progress sends none and delivers none.
	if got, _ := compareProgress(t, "go", "plain"); len(got.Events) != 0 || got.Err != "ok" {
		t.Fatalf("plain: %+v", got)
	}
	// A failure after events: the events arrive, then the public error.
	if got, _ := compareProgress(t, "go", "fail"); len(got.Events) != 1 || got.Err != "remote:busy," {
		t.Fatalf("fail: %+v", got)
	}
	// A flood under a low rate: the first events, then one marker that counts
	// every event lost. Both ends apply the rate the request carries.
	low := plugin.CallOptions{Progress: plugin.ProgressLimits{EventsPerSecond: 5}}
	got, _ = compareProgress(t, "go", "flood", low)
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
	// The guest sees the same rate the host set, defaults filled in.
	for _, c := range []struct {
		op      string
		options []plugin.CallOptions
		want    string
	}{
		{"limits", nil, `{"bytes":65536,"declared":true,"events":50}`},
		{"limits", []plugin.CallOptions{{Progress: plugin.ProgressLimits{EventsPerSecond: 7, BytesPerSecond: 900}}}, `{"bytes":900,"declared":true,"events":7}`},
		{"plainlimits", nil, `{"bytes":0,"declared":false,"events":0}`},
	} {
		if got, _ := compareProgress(t, "go", c.op, c.options...); got.Result != c.want {
			t.Errorf("%s: result %s, want %s", c.op, got.Result, c.want)
		}
	}
}

func TestProgressOptionsMatchGo(t *testing.T) {
	for _, limits := range []plugin.ProgressLimits{{EventsPerSecond: -1}, {BytesPerSecond: -1}, {EventsPerSecond: 1<<20 + 1}, {BytesPerSecond: 1<<30 + 1}} {
		got, want := compareProgress(t, "go", "stream", plugin.CallOptions{Progress: limits})
		if got.Err != "invalid," || want.Err != "invalid," {
			t.Errorf("%+v: C %s, Go %s", limits, got.Err, want.Err)
		}
	}
	// The largest accepted values are accepted by both.
	if got, _ := compareProgress(t, "go", "stream", plugin.CallOptions{Progress: plugin.ProgressLimits{EventsPerSecond: 1 << 20, BytesPerSecond: 1 << 30}}); got.Err != "ok" {
		t.Errorf("largest limits: %+v", got)
	}
}

// A guest that breaks the rules ends the session in both engines, with the same
// class of error.
func TestProgressViolationsMatchGo(t *testing.T) {
	for _, c := range []struct{ mode, op, want string }{
		{"undeclared", "plain", "protocol,"},
		{"wrongparent", "stream", "protocol,"},
		{"badkind", "stream", "protocol,"},
		{"streamprogress", "stream", "protocol,"},
		{"overtotal", "stream", "protocol,"},
		{"negative", "stream", "protocol,"},
		{"fraction", "stream", "protocol,"},
		{"bigtext", "stream", "protocol,"},
		{"dupkey", "stream", "protocol,"},
		{"unknownfield", "stream", "protocol,"},
		{"unknown", "stream", "protocol,"},
		{"notobject", "stream", "protocol,"},
		{"reverseresponse", "stream", "protocol,"},
		{"nullroot", "stream", "protocol,"},
		{"hellofirst", "stream", "protocol,"},
		{"garbage", "stream", "protocol,"},
		{"wrongid", "stream", "protocol,"},
		{"wrongversion", "stream", "mismatch,"},
		{"extrafield", "stream", "invalid,"},
		{"dupresponse", "stream", "invalid,"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			got, want := compareProgress(t, "raw:"+c.mode, c.op)
			if want.Err != c.want {
				t.Errorf("the Go session answered %q, want %q: the expectation is stale", want.Err, c.want)
			}
			if got.State == plugin.StateReady {
				t.Errorf("the session survived a violation: %+v", got)
			}
		})
	}
	// Not compared: a frame that arrives after the response. Go reads on in the
	// background, so that call succeeds and the session fails afterwards; the C
	// host reads synchronously and fails the call that saw the extra bytes.
	// A well-behaved raw guest is accepted, so the cases above fail for the
	// frames they send and not for the harness.
	if got, _ := compareProgress(t, "raw:ok", "stream"); got.Err != "ok" || len(got.Events) != 1 || got.Events[0].Text != "x" {
		t.Fatalf("raw guest: %+v", got)
	}
}

func TestCausesNameProtocolViolations(t *testing.T) {
	// Sanity: the helper's classes distinguish what the tests rely on.
	if causes(plugin.ErrProtocol) != "protocol," || causes(errors.New("x")) != "other" {
		t.Fatal("causes does not tell protocol violations apart")
	}
}
