package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/webong/ext/res/graph/store"
)

const behaviourNS = "behaviour.example"

func vertex(id string) store.Vertex { return store.Vertex{ID: id, Kind: behaviourNS + "/node"} }

// RunBehaviour checks what the fixtures cannot hold: live watch delivery,
// a watch that overflows its buffer, concurrent writers, and a closed store.
func RunBehaviour(t *testing.T, b Backend) {
	open := func(t *testing.T, dir string, o store.Options) store.Store {
		s := b.Open(t, dir, o)
		if err := s.Register(context.Background(), store.Schema{Namespace: behaviourNS, Version: "1"}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("watch streams later commits in cursor order", func(t *testing.T) {
		s := open(t, t.TempDir(), store.Options{})
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := s.Watch(ctx, behaviourNS, 0, 16)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if _, err := s.Apply(context.Background(), store.Transaction{Namespace: behaviourNS, Vertices: []store.Vertex{vertex(fmt.Sprintf("v%d", i))}}); err != nil {
				t.Fatal(err)
			}
		}
		for want := uint64(1); want <= 3; want++ {
			select {
			case change, ok := <-ch:
				if !ok || change.Cursor != want {
					t.Fatalf("change %+v (open %v), want cursor %d", change, ok, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("no change %d", want)
			}
		}
		cancel()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-deadline:
				t.Fatal("watch stayed open after cancel")
			}
		}
	})
	t.Run("a full watch closes", func(t *testing.T) {
		s := open(t, t.TempDir(), store.Options{})
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := s.Watch(ctx, behaviourNS, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			if _, err := s.Apply(context.Background(), store.Transaction{Namespace: behaviourNS, Vertices: []store.Vertex{vertex(fmt.Sprintf("v%d", i))}}); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.After(3 * time.Second)
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-deadline:
				t.Fatal("overflowing watch stayed open")
			}
		}
	})
	t.Run("concurrent writers lose nothing", func(t *testing.T) {
		s := open(t, t.TempDir(), store.Options{})
		defer s.Close()
		const writers = 8
		errs := make(chan error, writers)
		for i := 0; i < writers; i++ {
			go func(i int) {
				_, err := s.Apply(context.Background(), store.Transaction{Namespace: behaviourNS, Vertices: []store.Vertex{vertex(fmt.Sprintf("w%d", i))}})
				errs <- err
			}(i)
		}
		for i := 0; i < writers; i++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		snap, err := s.Snapshot(context.Background(), behaviourNS)
		if err != nil || len(snap.Vertices) != writers || snap.Cursor != writers || snap.Revision != writers {
			t.Fatalf("snapshot %+v, %v", snap, err)
		}
	})
	t.Run("a closed store is unavailable", func(t *testing.T) {
		s := open(t, t.TempDir(), store.Options{})
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
		if _, err := s.Apply(context.Background(), store.Transaction{Namespace: behaviourNS, Vertices: []store.Vertex{vertex("a")}}); store.Code(err) != "unavailable" {
			t.Fatalf("apply after close: %v", err)
		}
		if _, err := s.Snapshot(context.Background(), behaviourNS); store.Code(err) != "unavailable" {
			t.Fatalf("snapshot after close: %v", err)
		}
	})
	t.Run("close ends every watch", func(t *testing.T) {
		s := open(t, t.TempDir(), store.Options{})
		ch, err := s.Watch(context.Background(), "", 0, 4)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatal("watch delivered after close")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("watch stayed open after close")
		}
	})
}
