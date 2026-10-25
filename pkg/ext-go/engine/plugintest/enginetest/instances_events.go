package enginetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/guest/instance"
)

// eventScenario is an instance scenario with the events the Go manager is expected to report,
// when the test names them: the comparison with the C-backed manager then cannot agree on nothing.
type eventScenario struct {
	instanceScenario
	events []string
}

func plainInstances(capacity int) func() instance.Options[string] {
	return func() instance.Options[string] {
		return instance.Options[string]{Capacity: capacity, Validate: func(json.RawMessage) error { return nil },
			Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
				return string(c), func() error { return nil }, nil
			}}
	}
}

// eventScenarios are the error scenarios of TestInstanceErrorCausesMatchGo and the ones whose
// point is the order of the events: when an instance is retired, and when it is disposed of, which
// is when its last lease is released.
func eventScenarios() []eventScenario {
	var all []eventScenario
	for _, sc := range instanceScenarios() {
		all = append(all, eventScenario{instanceScenario: sc})
	}
	config := func(n int) json.RawMessage { return json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)) }
	return append(all,
		eventScenario{instanceScenario{"replace-while-leased", plainInstances(4),
			func(ctx context.Context, r instanceRig) []string {
				out := []string{Causes(r.m.Configure(ctx, "a", "r1", config(1)))}
				l, v, err := r.acquire("a")
				out = append(out, v, Causes(err), Causes(r.m.Configure(ctx, "a", "r2", config(2))))
				// The old instance is retired and lives on for its lease.
				out = append(out, Causes(l.Release()))
				l2, v2, err := r.acquire("a")
				if l2 != nil {
					out = append(out, Causes(l2.Release()))
				}
				return append(out, v2, Causes(err))
			}}, []string{
			"configured a r1", "configured a r2", "retired a r1", "disposed a r1", "disposed a r2"}},
		eventScenario{instanceScenario{"replace-unleased", plainInstances(4),
			func(ctx context.Context, r instanceRig) []string {
				return []string{Causes(r.m.Configure(ctx, "a", "r1", config(1))), Causes(r.m.Configure(ctx, "a", "r2", config(2))), Causes(r.m.Configure(ctx, "a", "r3", config(3)))}
			}}, []string{
			"configured a r1", "configured a r2", "retired a r1", "disposed a r1", "configured a r3", "retired a r2", "disposed a r2", "disposed a r3"}},
		eventScenario{instanceScenario{"remove-while-leased", plainInstances(4),
			func(ctx context.Context, r instanceRig) []string {
				out := []string{Causes(r.m.Configure(ctx, "a", "r1", config(1)))}
				l, _, err := r.acquire("a")
				out = append(out, Causes(err), Causes(r.m.Remove("a")))
				_, _, gone := r.acquire("a")
				return append(out, Causes(gone), Causes(l.Release()), Causes(r.m.Remove("a")))
			}}, nil},
		eventScenario{instanceScenario{"same-revision-again", plainInstances(4),
			func(ctx context.Context, r instanceRig) []string {
				return []string{
					Causes(r.m.Configure(ctx, "a", "r1", config(1))),
					Causes(r.m.Configure(ctx, "a", "r1", config(1))),
					Causes(r.m.Configure(ctx, "a", "r1", config(2))),
				}
			}}, []string{"configured a r1", "disposed a r1"}},
		eventScenario{instanceScenario{"capacity-counts-the-retiring", plainInstances(2),
			func(ctx context.Context, r instanceRig) []string {
				out := []string{Causes(r.m.Configure(ctx, "a", "r1", config(1)))}
				l, _, err := r.acquire("a")
				// The old instance lives on for its lease and still takes a place.
				out = append(out, Causes(err), Causes(r.m.Configure(ctx, "a", "r2", config(2))), Causes(r.m.Configure(ctx, "b", "r1", config(3))), Causes(l.Release()))
				return append(out, Causes(r.m.Configure(ctx, "b", "r1", config(3))))
			}}, []string{"configured a r1", "configured a r2", "retired a r1", "disposed a r1", "configured b r1", "disposed a r2", "disposed b r1"}},
		eventScenario{instanceScenario{"close-waits-for-leases", plainInstances(4),
			func(ctx context.Context, r instanceRig) []string {
				out := []string{Causes(r.m.Configure(ctx, "a", "r1", config(1))), Causes(r.m.Configure(ctx, "b", "r1", config(2)))}
				l, _, err := r.acquire("a")
				short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
				out = append(out, Causes(err), Causes(r.m.Close(short)), Causes(l.Release()), Causes(r.m.Close(ctx)))
				_, _, after := r.acquire("a")
				return append(out, Causes(after))
			}}, nil},
		eventScenario{instanceScenario{"several-keys", plainInstances(8),
			func(ctx context.Context, r instanceRig) []string {
				var out []string
				for i, key := range []string{"a", "b", "c"} {
					out = append(out, Causes(r.m.Configure(ctx, key, "r1", config(i))))
				}
				ls := make([]leaseLike, 0, 3)
				for _, key := range []string{"c", "a"} {
					l, _, err := r.acquire(key)
					out = append(out, Causes(err))
					ls = append(ls, l)
				}
				out = append(out, Causes(r.m.Configure(ctx, "b", "r2", config(9))), Causes(r.m.Remove("c")))
				for _, l := range ls {
					out = append(out, Causes(l.Release()))
				}
				return out
			}}, nil},
	)
}

// closeMarks brackets each Close in the event log. Close disposes of the instances concurrently,
// so the events between the marks arrive in any order; everything else is in a fixed order.
type closeMarks struct {
	Instances
	mark func(string)
}

func (c closeMarks) Close(ctx context.Context) error {
	c.mark("close>")
	err := c.Instances.Close(ctx)
	c.mark("<close")
	return err
}

// settled sorts the events between each pair of marks and drops the marks.
func settled(events []string) []string {
	var out, open []string
	inside := false
	for _, e := range events {
		switch {
		case e == "close>":
			inside, open = true, nil
		case e == "<close":
			sort.Strings(open)
			out, open, inside = append(out, open...), nil, false
		case inside:
			open = append(open, e)
		default:
			out = append(out, e)
		}
	}
	sort.Strings(open)
	return append(out, open...)
}

// InstanceObserverEventsMatchGo runs the scenarios through both managers and requires the
// same events, in the same order: configured, retired and disposed, with the key and revision.
func InstanceObserverEventsMatchGo(t *testing.T, e Engine) {
	if e.NewInstances == nil {
		t.Skip("the engine has no instance manager to compare")
	}
	for _, sc := range eventScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			var outs [2]string
			var goEvents []string
			for i, c := range []bool{false, true} {
				var mu sync.Mutex
				var events []string
				opts := sc.opts()
				opts.Observe = func(e instance.Event) {
					mu.Lock()
					defer mu.Unlock()
					events = append(events, fmt.Sprintf("%s %s %s", e.State, e.Key, e.Revision))
				}
				r, err := e.openRig(c, opts)
				if err != nil {
					t.Fatal(err)
				}
				r.m = closeMarks{Instances: r.m, mark: func(m string) {
					mu.Lock()
					defer mu.Unlock()
					events = append(events, m)
				}}
				result := sc.run(context.Background(), r)
				// A scenario that leaves a lease held would make Close wait; fail instead of hanging.
				closing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := r.m.Close(closing); errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("close did not finish: the scenario leaves a lease held")
				}
				cancel()
				mu.Lock()
				events = settled(events)
				outs[i] = fmt.Sprint(result) + "\n  " + strings.Join(events, "\n  ")
				if !c {
					goEvents = append([]string(nil), events...)
				}
				mu.Unlock()
			}
			if sc.events != nil && strings.Join(goEvents, ",") != strings.Join(sc.events, ",") {
				t.Fatalf("the Go manager reported\n  %s\nwant\n  %s", strings.Join(goEvents, "\n  "), strings.Join(sc.events, "\n  "))
			}
			if outs[0] != outs[1] {
				t.Fatalf("instance observer events differ\n go: %s\n c:  %s", outs[0], outs[1])
			}
		})
	}
}
