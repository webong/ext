//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
	"strings"
	"testing"
	"time"
)

// instances is the part of the Go and C instance managers that is shared.
type instances interface {
	Configure(context.Context, string, string, json.RawMessage) error
	Remove(string) error
	Close(context.Context) error
}

type leaseLike interface{ Release() error }

type instanceRig struct {
	m       instances
	acquire func(string) (leaseLike, string, error)
}

// disposeBoom is returned by a disposer so the test can see it survive.
var disposeBoom = errors.New("dispose boom")

func openRig(c bool, opts instance.Options[string]) (instanceRig, error) {
	if c {
		m, err := NewInstances(opts)
		if err != nil {
			return instanceRig{}, err
		}
		return instanceRig{m: m, acquire: func(k string) (leaseLike, string, error) {
			l, err := m.Acquire(k)
			if err != nil {
				return nil, "", err
			}
			return l, l.Value, nil
		}}, nil
	}
	m, err := instance.New(opts)
	if err != nil {
		return instanceRig{}, err
	}
	return instanceRig{m: m, acquire: func(k string) (leaseLike, string, error) {
		l, err := m.Acquire(k)
		if err != nil {
			return nil, "", err
		}
		return l, l.Value, nil
	}}, nil
}

// TestInstanceErrorCausesMatchGo runs the same sequence of operations through
// both managers and compares the class of every error.
func TestInstanceErrorCausesMatchGo(t *testing.T) {
	scenarios := []struct {
		name string
		opts func() instance.Options[string]
		run  func(ctx context.Context, r instanceRig) []string
	}{
		{"dispose-error-on-release",
			func() instance.Options[string] {
				return instance.Options[string]{Capacity: 2, Validate: func(json.RawMessage) error { return nil },
					Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
						return string(c), func() error { return disposeBoom }, nil
					}}
			},
			func(ctx context.Context, r instanceRig) []string {
				out := []string{causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`)))}
				l, _, err := r.acquire("a")
				out = append(out, causes(err))
				out = append(out, causes(r.m.Remove("a")), fmt.Sprint(errors.Is(l.Release(), disposeBoom)), fmt.Sprint(errors.Is(l.Release(), disposeBoom)))
				return out
			}},
		{"dispose-error-on-close",
			func() instance.Options[string] {
				return instance.Options[string]{Capacity: 2, Validate: func(json.RawMessage) error { return nil },
					Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
						return "v", func() error { return disposeBoom }, nil
					}}
			},
			func(ctx context.Context, r instanceRig) []string {
				out := []string{causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`))), causes(r.m.Configure(ctx, "b", "r1", json.RawMessage(`{}`)))}
				err := r.m.Close(ctx)
				out = append(out, causes(err), fmt.Sprint(errors.Is(err, disposeBoom)))
				out = append(out, causes(r.m.Close(ctx)), causes(r.m.Configure(ctx, "c", "r1", json.RawMessage(`{}`))))
				_, _, aerr := r.acquire("a")
				return append(out, causes(aerr))
			}},
		{"dispose-error-on-remove",
			func() instance.Options[string] {
				return instance.Options[string]{Capacity: 2, Validate: func(json.RawMessage) error { return nil },
					Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
						return "v", func() error { return disposeBoom }, nil
					}}
			},
			func(ctx context.Context, r instanceRig) []string {
				out := []string{causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`)))}
				err := r.m.Remove("a")
				return append(out, causes(err), fmt.Sprint(errors.Is(err, disposeBoom)), causes(r.m.Remove("a")))
			}},
		{"dispose-error-on-replace",
			func() instance.Options[string] {
				n := 0
				return instance.Options[string]{Capacity: 2, Validate: func(json.RawMessage) error { return nil },
					Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
						n++
						return fmt.Sprint(n), func() error { return disposeBoom }, nil
					}}
			},
			func(ctx context.Context, r instanceRig) []string {
				out := []string{causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`)))}
				err := r.m.Configure(ctx, "a", "r2", json.RawMessage(`{}`))
				out = append(out, causes(err), fmt.Sprint(errors.Is(err, disposeBoom)))
				l, v, aerr := r.acquire("a")
				if l != nil {
					out = append(out, fmt.Sprint(errors.Is(l.Release(), disposeBoom)))
				}
				return append(out, v, causes(aerr))
			}},
		{"create-fails-and-validate-fails",
			func() instance.Options[string] {
				return instance.Options[string]{Capacity: 2,
					Validate: func(c json.RawMessage) error {
						if strings.Contains(string(c), "bad") {
							return errors.New("bad config")
						}
						return nil
					},
					Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
						if strings.Contains(string(c), "fail") {
							return "", nil, errors.New("create failed")
						}
						return "v", func() error { return nil }, nil
					}}
			},
			func(ctx context.Context, r instanceRig) []string {
				return []string{
					causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{"bad":1}`))),
					causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{"fail":1}`))),
					causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{`))),
					causes(r.m.Configure(ctx, "", "r1", json.RawMessage(`{}`))),
					causes(r.m.Configure(ctx, "a", "", json.RawMessage(`{}`))),
					causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`))),
					causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`))),
					causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{"x":2}`))),
				}
			}},
		{"capacity-and-unknown-keys",
			func() instance.Options[string] {
				return instance.Options[string]{Capacity: 1, Validate: func(json.RawMessage) error { return nil },
					Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
						return "v", func() error { return nil }, nil
					}}
			},
			func(ctx context.Context, r instanceRig) []string {
				out := []string{causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`))), causes(r.m.Configure(ctx, "b", "r1", json.RawMessage(`{}`)))}
				_, _, err := r.acquire("missing")
				out = append(out, causes(err), causes(r.m.Remove("missing")), causes(r.m.Remove("a")), causes(r.m.Configure(ctx, "b", "r1", json.RawMessage(`{}`))))
				return out
			}},
		{"canceled-contexts",
			func() instance.Options[string] {
				return instance.Options[string]{Capacity: 2, Validate: func(json.RawMessage) error { return nil },
					Create: func(_ context.Context, _ string, c json.RawMessage) (string, func() error, error) {
						return "v", func() error { return nil }, nil
					}}
			},
			func(ctx context.Context, r instanceRig) []string {
				dead, cancel := context.WithCancel(ctx)
				cancel()
				out := []string{causes(r.m.Configure(dead, "a", "r1", json.RawMessage(`{}`))), causes(r.m.Configure(ctx, "a", "r1", json.RawMessage(`{}`)))}
				l, _, err := r.acquire("a")
				past, c2 := context.WithTimeout(ctx, time.Nanosecond)
				defer c2()
				time.Sleep(time.Millisecond)
				out = append(out, causes(err), causes(r.m.Close(past)))
				if l != nil {
					out = append(out, causes(l.Release()))
				}
				return append(out, causes(r.m.Close(ctx)))
			}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			var outs [2]string
			for i, c := range []bool{false, true} {
				r, err := openRig(c, sc.opts())
				if err != nil {
					t.Fatal(err)
				}
				outs[i] = fmt.Sprint(sc.run(context.Background(), r))
				_ = r.m.Close(context.Background())
			}
			if outs[0] != outs[1] {
				t.Fatalf("instance error causes differ\n  go: %s\n  c:  %s", outs[0], outs[1])
			}
		})
	}
}
