package ledger

import (
	"context"
	"sync"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func (s *NativeStore) Traverse(ctx context.Context, namespace, start string, maxDepth, maxVertices int) (out []Path, err error) {
	err = s.call(ctx, "traverse", map[string]any{"namespace": namespace, "start": start, "max_depth": maxDepth, "max_vertices": maxVertices}, &out)
	return
}

// OpenChild opens a described object owned by this store. Close the child before
// closing the store; C enforces that lifetime even for concurrent callers.
func (s *NativeStore) OpenChild(ctx context.Context, kind string, config map[string]any) (*library.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	in := make(map[string]any, len(config)+1)
	for k, v := range config {
		in[k] = v
	}
	in["store"] = s.object.Handle()
	return s.library.api.Open(kind, in, nil)
}

// NativeWatch retains terminal errors explicitly, unlike the compatibility
// channel API. Next and Close are safe to call concurrently.
type NativeWatch struct {
	mu     sync.Mutex
	object *library.Object
	closed bool
	done   chan struct{}
}

func (s *NativeStore) OpenWatch(ctx context.Context, namespace string, after uint64, buffer int) (*NativeWatch, error) {
	o, err := s.OpenChild(ctx, "watch", map[string]any{"namespace": namespace, "after": after, "buffer": buffer})
	if err != nil {
		return nil, err
	}
	return &NativeWatch{object: o, done: make(chan struct{})}, nil
}

func (w *NativeWatch) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	err := w.object.Close()
	if err == nil {
		w.closed = true
		close(w.done)
	}
	return err
}

func (w *NativeWatch) Next(ctx context.Context) (Change, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Change{}, err
		}
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return Change{}, ErrClosed
		}
		// Bound every native wait, allowing cancellation and concurrent Close to
		// complete without closing a handle underneath a call.
		poll, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
		var out Change
		err := call(poll, w.object, "next", map[string]any{}, &out)
		cancel()
		w.mu.Unlock()
		if Code(err) == "timeout" {
			continue
		}
		return out, err
	}
}

// Watch preserves the compatibility channel API. OpenWatch exposes terminal
// error details; Store.Close terminates these compatibility channels.
func (s *NativeStore) Watch(ctx context.Context, namespace string, after uint64, buffer int) (<-chan Change, error) {
	w, err := s.OpenWatch(ctx, namespace, after, buffer)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = w.Close()
		return nil, ErrClosed
	}
	if s.watchers == nil {
		s.watchers = make(map[*NativeWatch]struct{})
	}
	s.watchers[w] = struct{}{}
	s.mu.Unlock()
	out := make(chan Change)
	go func() {
		defer close(out)
		defer func() { _ = w.Close(); s.mu.Lock(); delete(s.watchers, w); s.mu.Unlock() }()
		for {
			change, err := w.Next(ctx)
			if err != nil {
				return
			}
			select {
			case out <- change:
			case <-ctx.Done():
				return
			case <-w.done:
				return
			}
		}
	}()
	return out, nil
}

var _ Store = (*NativeStore)(nil)
