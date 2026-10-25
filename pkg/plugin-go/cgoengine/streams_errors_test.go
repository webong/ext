//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
	"github.com/webong/ext/pkg/plugin-go/engine/guest/author"
	"github.com/webong/ext/pkg/plugin-go/engine/guest/stream"
	"github.com/webong/ext/pkg/plugin-go/engine/transport/inprocess"
	"strings"
	"sync"
	"testing"
	"time"
)

// scripted is a reader whose behavior a scenario describes.
type scripted struct {
	mu     sync.Mutex
	reads  int
	closes int
	read   func(ctx context.Context, n, limit int) ([]json.RawMessage, bool, error)
}

func (r *scripted) Read(ctx context.Context, limit int) ([]json.RawMessage, bool, error) {
	r.mu.Lock()
	r.reads++
	n := r.reads
	r.mu.Unlock()
	return r.read(ctx, n, limit)
}
func (r *scripted) Close() error { r.mu.Lock(); r.closes++; r.mu.Unlock(); return nil }
func (r *scripted) counts() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("reads=%d closes=%d", r.reads, r.closes)
}

func item(v int) json.RawMessage { return json.RawMessage(fmt.Sprintf(`{"v":%d}`, v)) }

type streamScenario struct {
	name     string
	capacity int
	maxAge   time.Duration
	scope    func(context.Context, plugin.Request) (string, error)
	open     func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error)
	steps    func(ctx context.Context, rig *streamHarness) []string
	readers  func() []*scripted // readers handed out by open, in order
}

type streamHarness struct {
	caller author.Caller
}

func (h *streamHarness) open(ctx context.Context) (string, string) {
	r, err := author.Call(ctx, h.caller, stream.OpenMethod(), stream.OpenRequest{Parameters: json.RawMessage(`{}`)})
	if err != nil {
		return "", "open:" + describe(err)
	}
	return r.ID, "open:ok"
}
func (h *streamHarness) read(ctx context.Context, id string, seq uint64, limit int) string {
	b, err := author.Call(ctx, h.caller, stream.ReadMethod(), stream.ReadRequest{ID: id, Sequence: seq, Limit: limit})
	if err != nil {
		return "read:" + describe(err)
	}
	raw, _ := json.Marshal(b)
	return "read:" + string(raw)
}
func (h *streamHarness) close(ctx context.Context, id string) string {
	_, err := author.Call(ctx, h.caller, stream.CloseMethod(), stream.CloseRequest{ID: id})
	return "close:" + describe(err)
}

// describe is the error class plus any remote code, ignoring message text.
func describe(err error) string {
	c := causes(err)
	return strings.TrimSuffix(c, ",")
}

func streamScenarios() []streamScenario {
	allow := func(context.Context, plugin.Request) (string, error) { return "tenant", nil }
	three := func(r *scripted) {
		r.read = func(ctx context.Context, n, limit int) ([]json.RawMessage, bool, error) {
			out := []json.RawMessage{}
			for i := 0; i < limit && i < 2; i++ {
				out = append(out, item(n*10+i))
			}
			return out, n >= 3, nil
		}
	}
	single := func(setup func(*scripted)) (func() []*scripted, func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error)) {
		var mu sync.Mutex
		var all []*scripted
		return func() []*scripted { mu.Lock(); defer mu.Unlock(); return all },
			func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) {
				r := &scripted{}
				setup(r)
				mu.Lock()
				all = append(all, r)
				mu.Unlock()
				return r, nil
			}
	}
	mk := func(name string, capacity int, setup func(*scripted), steps func(context.Context, *streamHarness) []string) streamScenario {
		readers, open := single(setup)
		return streamScenario{name: name, capacity: capacity, maxAge: time.Minute, scope: allow, open: open, readers: readers, steps: steps}
	}
	return []streamScenario{
		mk("sequence-and-done", 2, three, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 2, 2), h.read(ctx, id, 1, 2), h.read(ctx, id, 1, 2), h.read(ctx, id, 2, 2), h.read(ctx, id, 3, 2), h.read(ctx, id, 4, 2), h.close(ctx, id)}
		}),
		mk("limits", 2, three, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 1, 0), h.read(ctx, id, 1, -1), h.read(ctx, id, 1, stream.MaxItems+1), h.read(ctx, id, 1, 1), h.read(ctx, id, 2, stream.MaxItems)}
		}),
		mk("unknown-ids", 2, three, func(ctx context.Context, h *streamHarness) []string {
			return []string{h.read(ctx, "nope", 1, 1), h.read(ctx, "", 1, 1), h.close(ctx, "nope"), h.close(ctx, "")}
		}),
		mk("close-then-read", 2, three, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.close(ctx, id), h.close(ctx, id), h.read(ctx, id, 1, 1)}
		}),
		mk("capacity", 1, three, func(ctx context.Context, h *streamHarness) []string {
			a, o1 := h.open(ctx)
			_, o2 := h.open(ctx)
			c := h.close(ctx, a)
			_, o3 := h.open(ctx)
			return []string{o1, o2, c, o3}
		}),
		mk("reader-error-closes-stream", 2, func(r *scripted) {
			r.read = func(context.Context, int, int) ([]json.RawMessage, bool, error) {
				return nil, false, errors.New("disk on fire")
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 1, 1), h.read(ctx, id, 1, 1)}
		}),
		mk("reader-remote-error", 2, func(r *scripted) {
			r.read = func(context.Context, int, int) ([]json.RawMessage, bool, error) {
				return nil, false, &plugin.RemoteError{Code: "quota", Message: "slow down"}
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 1, 1)}
		}),
		mk("reader-exceeds-limit", 2, func(r *scripted) {
			r.read = func(context.Context, int, int) ([]json.RawMessage, bool, error) {
				return []json.RawMessage{item(1), item(2), item(3)}, false, nil
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 1, 2), h.read(ctx, id, 1, 2)}
		}),
		mk("reader-invalid-item", 2, func(r *scripted) {
			r.read = func(context.Context, int, int) ([]json.RawMessage, bool, error) {
				return []json.RawMessage{json.RawMessage(`{`)}, false, nil
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 1, 2), h.read(ctx, id, 1, 2)}
		}),
		mk("reader-oversize-batch", 2, func(r *scripted) {
			big := json.RawMessage(`"` + strings.Repeat("x", 600<<10) + `"`)
			r.read = func(context.Context, int, int) ([]json.RawMessage, bool, error) {
				return []json.RawMessage{big, big}, false, nil
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 1, 2), h.read(ctx, id, 1, 2)}
		}),
		mk("final-batch-with-done", 2, func(r *scripted) {
			r.read = func(context.Context, int, int) ([]json.RawMessage, bool, error) {
				return []json.RawMessage{item(1)}, true, nil
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			return []string{o, h.read(ctx, id, 1, 5), h.read(ctx, id, 2, 5)}
		}),
		mk("cancelled-read", 2, func(r *scripted) {
			r.read = func(ctx context.Context, n, limit int) ([]json.RawMessage, bool, error) {
				<-ctx.Done()
				return nil, false, ctx.Err()
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancel()
			return []string{o, h.read(short, id, 1, 1), h.read(ctx, id, 1, 1)}
		}),
		mk("close-unblocks-read", 2, func(r *scripted) {
			r.read = func(ctx context.Context, n, limit int) ([]json.RawMessage, bool, error) {
				<-ctx.Done()
				return nil, false, ctx.Err()
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			blocked := make(chan string, 1)
			go func() { blocked <- h.read(ctx, id, 1, 1) }()
			time.Sleep(50 * time.Millisecond)
			closed := h.close(ctx, id)
			var read string
			select {
			case read = <-blocked:
			case <-time.After(5 * time.Second):
				read = "read:HUNG"
			}
			return []string{o, closed, read, h.read(ctx, id, 1, 1)}
		}),
		mk("concurrent-reads", 2, func(r *scripted) {
			r.read = func(ctx context.Context, n, limit int) ([]json.RawMessage, bool, error) {
				select {
				case <-time.After(150 * time.Millisecond):
					return []json.RawMessage{item(n)}, false, nil
				case <-ctx.Done():
					return nil, false, ctx.Err()
				}
			}
		}, func(ctx context.Context, h *streamHarness) []string {
			id, o := h.open(ctx)
			first := make(chan string, 1)
			go func() { first <- h.read(ctx, id, 1, 1) }()
			time.Sleep(40 * time.Millisecond)
			// The second read waits its turn on the same stream; both ask for sequence 1,
			// so exactly one may succeed.
			second := h.read(ctx, id, 1, 1)
			return []string{o, sortedPair(<-first, second)}
		}),
		{name: "scope-denies-open", capacity: 2, maxAge: time.Minute,
			scope: func(context.Context, plugin.Request) (string, error) { return "", errors.New("no tenant") },
			open: func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) {
				return &scripted{read: func(context.Context, int, int) ([]json.RawMessage, bool, error) { return nil, true, nil }}, nil
			},
			readers: func() []*scripted { return nil },
			steps: func(ctx context.Context, h *streamHarness) []string {
				_, o := h.open(ctx)
				return []string{o, h.read(ctx, "x", 1, 1), h.close(ctx, "x")}
			}},
		{name: "open-fails", capacity: 2, maxAge: time.Minute, scope: allow,
			open: func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) {
				return nil, errors.New("cannot open")
			},
			readers: func() []*scripted { return nil },
			steps: func(ctx context.Context, h *streamHarness) []string {
				_, o := h.open(ctx)
				_, o2 := h.open(ctx)
				return []string{o, o2}
			}},
		{name: "expires", capacity: 2, maxAge: 40 * time.Millisecond, scope: allow,
			open: func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) {
				return &scripted{read: func(context.Context, int, int) ([]json.RawMessage, bool, error) {
					return []json.RawMessage{item(1)}, false, nil
				}}, nil
			},
			readers: func() []*scripted { return nil },
			steps: func(ctx context.Context, h *streamHarness) []string {
				id, o := h.open(ctx)
				first := h.read(ctx, id, 1, 1)
				time.Sleep(150 * time.Millisecond)
				return []string{o, first, h.read(ctx, id, 2, 1), h.close(ctx, id)}
			}},
	}
}

func newStreamRig(t *testing.T, c bool, sc streamScenario) (*streamHarness, func()) {
	t.Helper()
	opts := stream.Options{Capacity: sc.capacity, MaxAge: sc.maxAge, Scope: sc.scope, Open: sc.open}
	r, err := author.New(plugin.Identity{ID: "example/stream", Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	var closers []func()
	authorize := func(context.Context, plugin.Request) error { return nil }
	var backend plugin.Backend
	var open func(context.Context, plugin.Descriptor, plugin.Options) (author.Caller, error)
	if c {
		svc, err := NewStreams(opts)
		if err != nil {
			t.Fatal(err)
		}
		closers = append(closers, func() { svc.Close() })
		if err := svc.Register(r); err != nil {
			t.Fatal(err)
		}
		g, err := GuestFromRegistry(r, author.Options{Authorize: authorize})
		if err != nil {
			t.Fatal(err)
		}
		closers = append(closers, g.Destroy)
		backend, err = inprocess.New(g)
		if err != nil {
			t.Fatal(err)
		}
		open = func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (author.Caller, error) {
			s, err := Open(ctx, d, o)
			if s != nil {
				closers = append(closers, func() { s.Abort() })
			}
			return s, err
		}
	} else {
		svc, err := stream.New(opts)
		if err != nil {
			t.Fatal(err)
		}
		closers = append(closers, func() { svc.Close() })
		if err := svc.Register(r); err != nil {
			t.Fatal(err)
		}
		g, err := r.Guest(author.Options{Authorize: authorize})
		if err != nil {
			t.Fatal(err)
		}
		backend, err = inprocess.New(g)
		if err != nil {
			t.Fatal(err)
		}
		open = func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (author.Caller, error) {
			s, err := plugin.Open(ctx, d, o)
			if s != nil {
				closers = append(closers, func() { s.Abort() })
			}
			return s, err
		}
	}
	caller, err := open(context.Background(), r.Descriptor(), plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &streamHarness{caller: caller}, func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
}

// TestStreamErrorCausesMatchGo drives scripted readers through the C stream
// service and the Go one and compares every result and error class, plus how
// often each reader was read and closed.
func TestStreamErrorCausesMatchGo(t *testing.T) {
	for _, name := range func() []string {
		var n []string
		for _, s := range streamScenarios() {
			n = append(n, s.name)
		}
		return n
	}() {
		t.Run(name, func(t *testing.T) {
			var outs [2]string
			for i, c := range []bool{false, true} {
				var sc streamScenario
				for _, s := range streamScenarios() { // fresh readers per implementation
					if s.name == name {
						sc = s
					}
				}
				h, done := newStreamRig(t, c, sc)
				steps := sc.steps(context.Background(), h)
				done()
				var counts []string
				for _, r := range sc.readers() {
					counts = append(counts, r.counts())
				}
				if name == "cancelled-read" {
					steps = interrupted(steps)
				}
				outs[i] = fmt.Sprintf("%q %v", steps, counts)
			}
			if outs[0] != outs[1] {
				t.Fatalf("stream behavior differs\n  go: %s\n  c:  %s", outs[0], outs[1])
			}
		})
	}
}

// sortedPair makes two racing results comparable without caring which won.
func sortedPair(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + " | " + b
}

// interrupted folds the outcomes of a read cut short by a client deadline. Whether
// the guest's error reply or the client's own deadline notification arrives first
// is a race in both implementations: the read then fails as a deadline (and the
// session fails with it) or as an operation error (and the session survives), and
// the stream is gone either way.
func interrupted(steps []string) []string {
	fold := map[string]string{
		"read:deadline":                "read:interrupted",
		"read:remote:operation_failed": "read:interrupted",
		"read:closed":                  "read:gone",
		"read:remote:denied":           "read:gone",
	}
	out := make([]string, len(steps))
	for i, s := range steps {
		if f, ok := fold[s]; ok {
			s = f
		}
		out[i] = s
	}
	return out
}
