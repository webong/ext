// Package inference guest-side stream bridging for the stream and synthesize
// operations, which open ext.stream instances.
package inference

import (
	"context"
	"encoding/json"
	"sync"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/stream"
	"github.com/webong/ext/res/inference"
)

// genReader bridges a push-based generation stream to a pull-based
// stream.Reader. Events are buffered under a mutex; Read drains up to limit
// events and blocks until generation completes, an error occurs, or the
// reader is closed.
type genReader struct {
	mu     sync.Mutex
	events []inference.GenerationEvent
	notify chan struct{} // signaled when events arrive, generation completes, or close
	done   bool
	err    error
	closed bool
}

func newGenReader() *genReader { return &genReader{notify: make(chan struct{}, 1)} }

func (r *genReader) push(event inference.GenerationEvent) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *genReader) finish(err error) {
	r.mu.Lock()
	r.done = true
	r.err = err
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *genReader) Read(ctx context.Context, limit int) ([]json.RawMessage, bool, error) {
	if limit < 1 {
		return nil, false, plugin.ErrInvalid
	}
	r.mu.Lock()
	for len(r.events) == 0 && !r.done && !r.closed {
		notify := r.notify
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-notify:
		}
		r.mu.Lock()
	}
	n := limit
	if n > len(r.events) {
		n = len(r.events)
	}
	batch := make([]json.RawMessage, 0, n)
	for i := 0; i < n; i++ {
		data, err := json.Marshal(r.events[i])
		if err != nil {
			r.mu.Unlock()
			return batch, false, err
		}
		batch = append(batch, data)
	}
	r.events = r.events[n:]
	done := r.done && len(r.events) == 0
	finalErr := r.err
	r.mu.Unlock()
	if done && finalErr != nil {
		return batch, true, finalErr
	}
	return batch, done, nil
}

func (r *genReader) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
	return nil
}

// synthReader bridges a push-based synthesis byte stream to a pull-based
// stream.Reader. Each Read returns one batch of audio bytes.
type synthReader struct {
	mu     sync.Mutex
	chunks [][]byte
	notify chan struct{}
	done   bool
	err    error
	closed bool
}

func newSynthReader() *synthReader { return &synthReader{notify: make(chan struct{}, 1)} }

func (r *synthReader) push(chunk []byte) {
	r.mu.Lock()
	r.chunks = append(r.chunks, append([]byte(nil), chunk...))
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *synthReader) finish(err error) {
	r.mu.Lock()
	r.done = true
	r.err = err
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *synthReader) Read(ctx context.Context, limit int) ([]json.RawMessage, bool, error) {
	if limit < 1 {
		return nil, false, plugin.ErrInvalid
	}
	r.mu.Lock()
	for len(r.chunks) == 0 && !r.done && !r.closed {
		notify := r.notify
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-notify:
		}
		r.mu.Lock()
	}
	n := limit
	if n > len(r.chunks) {
		n = len(r.chunks)
	}
	batch := make([]json.RawMessage, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, r.chunks[i])
	}
	r.chunks = r.chunks[n:]
	done := r.done && len(r.chunks) == 0
	finalErr := r.err
	r.mu.Unlock()
	if done && finalErr != nil {
		return batch, true, finalErr
	}
	return batch, done, nil
}

func (r *synthReader) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
	return nil
}

var _ = stream.Contract
