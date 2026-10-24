//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

// eventLog keeps the observer events of one session: everything but the duration.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (r *eventLog) observe(_ context.Context, e plugin.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf("%s id=%s/%s contract=%s/%s op=%q request=%q code=%s", e.Stage, e.Identity.ID, e.Identity.Revision, e.Contract.Name, e.Contract.Version, e.Operation, e.RequestID, e.Code))
}

func (r *eventLog) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.events, "\n  ")
}

// TestSessionObserverEventsMatchGo drives the scenarios of TestSessionErrorCausesMatchGo
// through the C-backed and the pure-Go session with an observer, and requires the same
// events in the same order: the stage, the identity, the contract and operation (blank when
// they are not valid names), the request id and the code.
func TestSessionObserverEventsMatchGo(t *testing.T) {
	for _, sc := range errorScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			ref := testDescriptor().Contracts[0].ContractRef
			collect := func(open func(context.Context, plugin.Descriptor, plugin.Options) (session, error)) string {
				b := sc.backend()
				o := referenceOptions(b)
				if sc.options != nil {
					sc.options(&o)
				}
				rec := &eventLog{}
				o.Observer = rec.observe
				s, err := open(context.Background(), b.d, o)
				if err != nil {
					return "open:" + causes(err) + "\n  " + rec.text()
				}
				sc.run(t, s, ref)
				s.Abort()
				return rec.text()
			}
			goOut := collect(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				return plugin.Open(ctx, d, o)
			})
			cOut := collect(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
				return Open(ctx, d, o)
			})
			if goOut != cOut {
				t.Fatalf("observer events differ\n go:\n  %s\n c:\n  %s", goOut, cOut)
			}
		})
	}
}

// TestOpenObserverEventsMatchGo does the same for the ways admitting a plugin can fail: the
// verify, connect and handshake events, and their codes.
func TestOpenObserverEventsMatchGo(t *testing.T) {
	for _, c := range openErrorCases() {
		t.Run(c.name, func(t *testing.T) {
			run := func(open func(context.Context, plugin.Descriptor, plugin.Options) (session, error)) string {
				b := c.backend()
				o := referenceOptions(b)
				if c.options != nil {
					c.options(&o)
				}
				rec := &eventLog{}
				o.Observer = rec.observe
				ctx := context.Background()
				if c.name == "canceled-context" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				s, err := open(ctx, testDescriptor(), o)
				if s != nil {
					s.Abort()
				}
				return causes(err) + "\n  " + rec.text()
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
				t.Fatalf("observer events differ\n go:\n  %s\n c:\n  %s", goOut, cOut)
			}
		})
	}
}

// TestDrainObserverEventsMatchGo reports a call refused while the session drains, and the call
// that was in flight when the drain began.
func TestDrainObserverEventsMatchGo(t *testing.T) {
	run := func(open func(context.Context, plugin.Descriptor, plugin.Options) (session, error)) string {
		release := make(chan struct{})
		started := make(chan struct{}, 1)
		var first atomic.Bool
		b := &testBackend{d: testDescriptor(), invoke: func(ctx context.Context, r plugin.Request) (plugin.Response, error) {
			if first.CompareAndSwap(false, true) {
				started <- struct{}{}
				<-release
			}
			return plugin.Response{APIVersion: plugin.APIVersion, ID: r.ID, Payload: json.RawMessage("null")}, nil
		}}
		o := referenceOptions(b)
		rec := &eventLog{}
		o.Observer = rec.observe
		s, err := open(context.Background(), b.d, o)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Abort()
		ref := b.d.Contracts[0].ContractRef
		inflight := make(chan string, 1)
		go func() { inflight <- one(context.Background(), s, ref, "inspect") }()
		<-started
		closed := make(chan string, 1)
		go func() { closed <- causes(s.Close(context.Background())) }()
		for i := 0; i < 2000 && s.State() != plugin.StateDraining; i++ {
			time.Sleep(time.Millisecond)
		}
		during := one(context.Background(), s, ref, "inspect")
		close(release)
		return fmt.Sprintf("inflight=%s during=%s close=%s\n  %s", <-inflight, during, <-closed, rec.text())
	}
	goOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
		return plugin.Open(ctx, d, o)
	})
	cOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (session, error) {
		return Open(ctx, d, o)
	})
	if goOut != cOut {
		t.Fatalf("observer events differ\n go:\n  %s\n c:\n  %s", goOut, cOut)
	}
}

// TestEventCodeFollowsTheContext pins what the engine's cancel signal and its deadline cannot
// tell apart: when the call's context is done, the event says why the context ended. On Windows
// the engine often sees the Go deadline's cancel signal before its own deadline (a silent child
// at the handshake reported "canceled" where the Go session says "deadline_exceeded").
func TestEventCodeFollowsTheContext(t *testing.T) {
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		code string
		want string
	}{
		{"timeout seen as a cancellation", expired, "canceled", "deadline_exceeded"},
		{"timeout seen as a timeout", expired, "deadline_exceeded", "deadline_exceeded"},
		{"cancellation seen as a timeout", canceled, "deadline_exceeded", "canceled"},
		{"cancellation seen as a cancellation", canceled, "canceled", "canceled"},
		{"engine's own deadline, context still live", context.Background(), "deadline_exceeded", "deadline_exceeded"},
		{"engine's cancel, context still live", context.Background(), "canceled", "canceled"},
		{"other codes are the engine's", expired, "mismatch", "mismatch"},
		{"success", expired, "ok", "ok"},
	} {
		if got := settledEventCode(c.ctx, c.code); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
