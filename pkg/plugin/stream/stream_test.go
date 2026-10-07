package stream_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/author"
	"github.com/webong/ext/pkg/plugin/inprocess"
	"github.com/webong/ext/pkg/plugin/stream"
)

type reader struct{ closed atomic.Int32 }

func (r *reader) Read(context.Context, int) ([]json.RawMessage, bool, error) {
	return []json.RawMessage{json.RawMessage(`{"value":1}`)}, true, nil
}
func (r *reader) Close() error { r.closed.Add(1); return nil }
func TestPullAndScope(t *testing.T) {
	rr := &reader{}
	scope := "one"
	svc, err := stream.New(stream.Options{Capacity: 2, MaxAge: time.Second, Scope: func(context.Context, plugin.Request) (string, error) { return scope, nil }, Open: func(context.Context, plugin.Request, json.RawMessage) (stream.Reader, error) { return rr, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	r, _ := author.New(plugin.Identity{ID: "example/stream", Revision: "r1"})
	if err := svc.Register(r); err != nil {
		t.Fatal(err)
	}
	g, _ := r.Guest(author.Options{Authorize: func(context.Context, plugin.Request) error { return nil }})
	b, _ := inprocess.New(g)
	s, err := plugin.Open(context.Background(), r.Descriptor(), plugin.Options{Verify: func(context.Context, plugin.Descriptor) error { return nil }, Authorize: func(context.Context, plugin.Request) error { return nil }, Connect: func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return b, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	ctx := context.Background()
	opened, err := author.Call(ctx, s, stream.OpenMethod(), stream.OpenRequest{Parameters: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	scope = "other"
	if _, err := author.Call(ctx, s, stream.ReadMethod(), stream.ReadRequest{ID: opened.ID, Sequence: 1, Limit: 1}); err == nil {
		t.Fatal("scope bypass")
	}
	scope = "one"
	batch, err := author.Call(ctx, s, stream.ReadMethod(), stream.ReadRequest{ID: opened.ID, Sequence: 1, Limit: 1})
	if err != nil || !batch.Done || len(batch.Items) != 1 {
		t.Fatal(batch, err)
	}
	if rr.closed.Load() != 1 {
		t.Fatal("leaked stream")
	}
	if _, err := author.Call(ctx, s, stream.ReadMethod(), stream.ReadRequest{ID: opened.ID, Sequence: 1, Limit: 1}); err == nil {
		t.Fatal("replayed closed stream")
	}
}
