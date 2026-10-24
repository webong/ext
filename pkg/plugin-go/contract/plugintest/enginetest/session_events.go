package enginetest

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

// EventLog keeps the observer events of one session: everything but the duration.
type EventLog struct {
	mu     sync.Mutex
	events []string
}

func (r *EventLog) Observe(_ context.Context, e plugin.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf("%s id=%s/%s contract=%s/%s op=%q request=%q code=%s", e.Stage, e.Identity.ID, e.Identity.Revision, e.Contract.Name, e.Contract.Version, e.Operation, e.RequestID, e.Code))
}

func (r *EventLog) Text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.events, "\n  ")
}

// SessionObserverEventsMatchGo drives the scenarios of TestSessionErrorCausesMatchGo
// through the C-backed and the pure-Go session with an observer, and requires the same
// events in the same order: the stage, the identity, the contract and operation (blank when
// they are not valid names), the request id and the code.
func SessionObserverEventsMatchGo(t *testing.T, e Engine) {
	if e.Open == nil {
		t.Skip("the engine has no session to compare")
	}
	for _, sc := range errorScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			ref := testDescriptor().Contracts[0].ContractRef
			collect := func(open func(context.Context, plugin.Descriptor, plugin.Options) (Session, error)) string {
				b := sc.backend()
				o := referenceOptions(b)
				if sc.options != nil {
					sc.options(&o)
				}
				rec := &EventLog{}
				o.Observer = rec.Observe
				s, err := open(context.Background(), b.d, o)
				if err != nil {
					return "open:" + Causes(err) + "\n  " + rec.Text()
				}
				sc.run(t, s, ref)
				s.Abort()
				return rec.Text()
			}
			goOut := collect(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error) {
				return plugin.Open(ctx, d, o)
			})
			cOut := collect(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error) {
				return e.Open(ctx, d, o)
			})
			if goOut != cOut {
				t.Fatalf("observer events differ\n go:\n  %s\n c:\n  %s", goOut, cOut)
			}
		})
	}
}

// OpenObserverEventsMatchGo does the same for the ways admitting a plugin can fail: the
// verify, connect and handshake events, and their codes.
func OpenObserverEventsMatchGo(t *testing.T, e Engine) {
	if e.Open == nil {
		t.Skip("the engine has no session to compare")
	}
	for _, c := range openErrorCases() {
		t.Run(c.name, func(t *testing.T) {
			run := func(open func(context.Context, plugin.Descriptor, plugin.Options) (Session, error)) string {
				b := c.backend()
				o := referenceOptions(b)
				if c.options != nil {
					c.options(&o)
				}
				rec := &EventLog{}
				o.Observer = rec.Observe
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
				return Causes(err) + "\n  " + rec.Text()
			}
			goOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error) {
				s, err := plugin.Open(ctx, d, o)
				if err != nil {
					return nil, err
				}
				return s, nil
			})
			cOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error) {
				s, err := e.Open(ctx, d, o)
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

// DrainObserverEventsMatchGo reports a call refused while the session drains, and the call
// that was in flight when the drain began.
func DrainObserverEventsMatchGo(t *testing.T, e Engine) {
	if e.Open == nil {
		t.Skip("the engine has no session to compare")
	}
	run := func(open func(context.Context, plugin.Descriptor, plugin.Options) (Session, error)) string {
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
		rec := &EventLog{}
		o.Observer = rec.Observe
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
		go func() { closed <- Causes(s.Close(context.Background())) }()
		for i := 0; i < 2000 && s.State() != plugin.StateDraining; i++ {
			time.Sleep(time.Millisecond)
		}
		during := one(context.Background(), s, ref, "inspect")
		close(release)
		return fmt.Sprintf("inflight=%s during=%s close=%s\n  %s", <-inflight, during, <-closed, rec.Text())
	}
	goOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error) {
		return plugin.Open(ctx, d, o)
	})
	cOut := run(func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error) {
		return e.Open(ctx, d, o)
	})
	if goOut != cOut {
		t.Fatalf("observer events differ\n go:\n  %s\n c:\n  %s", goOut, cOut)
	}
}
