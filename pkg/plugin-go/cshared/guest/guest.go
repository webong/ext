// Package guest binds Go guests to the CTX C ABI. A c-shared main package
// exports four tiny cgo wrappers; see examples/plugin-runtimes/cshared.
package guest

import (
	"context"
	"encoding/json"
	"sync"
	"unsafe"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin-cshared/abi"
)

type Factory func(context.Context) (plugin.Backend, error)
type Options struct {
	// Capacity bounds simultaneous handles and pending factories. Zero uses 64.
	Capacity int
}

type Server struct {
	mu       sync.Mutex
	factory  Factory
	capacity int
	active   int
	next     uint64
	entries  map[uint64]*entry
}
type entry struct {
	mu         sync.Mutex
	backend    plugin.Backend
	ctx        context.Context
	cancel     context.CancelFunc
	descriptor plugin.Descriptor
	hello      bool
}

func New(factory Factory, opts Options) (*Server, error) {
	if factory == nil || opts.Capacity < 0 {
		return nil, plugin.ErrInvalid
	}
	if opts.Capacity == 0 {
		opts.Capacity = 64
	}
	return &Server{factory: factory, capacity: opts.Capacity, entries: make(map[uint64]*entry)}, nil
}

// Open returns an opaque nonzero handle or zero on startup/capacity failure.
// Factories must honor context cancellation and return independent backends.
func (s *Server) Open() uint64 {
	s.mu.Lock()
	if s.active >= s.capacity || s.next == ^uint64(0) {
		s.mu.Unlock()
		return 0
	}
	s.active++
	s.next++
	id := s.next
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), plugin.DefaultTimeout)
	b, err := s.factory(ctx)
	if err == nil {
		err = ctx.Err()
	}
	cancel()
	if err != nil || b == nil {
		if b != nil {
			_ = b.Close()
		}
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
		return 0
	}
	lifetime, stop := context.WithCancel(context.Background())
	s.mu.Lock()
	s.entries[id] = &entry{backend: b, ctx: lifetime, cancel: stop}
	s.mu.Unlock()
	return id
}

// Close stops admission, cancels the handle, waits for its current call and
// releases resources once. It is safe for a foreign host to call concurrently.
func (s *Server) Close(handle uint64) {
	s.mu.Lock()
	e := s.entries[handle]
	delete(s.entries, handle)
	s.mu.Unlock()
	if e == nil {
		return
	}
	e.cancel()
	e.mu.Lock()
	_ = e.backend.Close()
	e.mu.Unlock()
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
}

// Call performs bounded strict decoding and enforces handshake-first ordering.
// A nonzero status conveys no private Go error text across the native boundary.
func (s *Server) Call(handle uint64, operation uint32, request []byte) ([]byte, uint32) {
	if len(request) == 0 || len(request) > plugin.MaxFrameBytes {
		return nil, abi.Invalid
	}
	s.mu.Lock()
	e := s.entries[handle]
	s.mu.Unlock()
	if e == nil {
		return nil, abi.Closed
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return nil, abi.Closed
	}
	var result any
	switch operation {
	case abi.Handshake:
		var hello abi.Hello
		if plugin.Decode(request, &hello) != nil || hello.Deadline.IsZero() {
			return nil, abi.Invalid
		}
		ctx, cancel := context.WithDeadline(e.ctx, hello.Deadline)
		defer cancel()
		ctx, limit := context.WithTimeout(ctx, plugin.DefaultTimeout)
		defer limit()
		if ctx.Err() != nil {
			return nil, abi.Failed
		}
		d, err := e.backend.Handshake(ctx)
		if err != nil || ctx.Err() != nil || d.Validate() != nil {
			return nil, abi.Failed
		}
		if e.hello && plugin.MatchHandshake(e.descriptor, d) != nil {
			return nil, abi.Failed
		}
		e.descriptor, e.hello = d.Clone(), true
		result = d
	case abi.Invoke:
		if !e.hello {
			return nil, abi.Invalid
		}
		var req plugin.Request
		if plugin.Decode(request, &req) != nil || plugin.ValidateRequest(e.descriptor, req) != nil {
			return nil, abi.Invalid
		}
		ctx, cancel := context.WithDeadline(e.ctx, req.Deadline)
		defer cancel()
		ctx, limit := context.WithTimeout(ctx, plugin.DefaultTimeout)
		defer limit()
		if ctx.Err() != nil {
			return nil, abi.Failed
		}
		resp, err := e.backend.Invoke(ctx, req)
		if err != nil || ctx.Err() != nil || resp.Validate(req.ID) != nil {
			return nil, abi.Failed
		}
		result = resp
	default:
		return nil, abi.Invalid
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > plugin.MaxFrameBytes {
		return nil, abi.Failed
	}
	return data, abi.OK
}

// CallInto is the cgo export bridge. Pointers must refer to caller-owned valid
// buffers of the declared lengths, with no overlap. Pointer validity cannot be
// checked by Go; only trusted native hosts may call this ABI. No pointer escapes.
func (s *Server) CallInto(handle uint64, operation uint32, request unsafe.Pointer, requestLen uint32, response unsafe.Pointer, capacity uint32, responseLen *uint32) uint32 {
	if responseLen == nil {
		return abi.Invalid
	}
	*responseLen = 0
	if request == nil || response == nil || requestLen == 0 || requestLen > plugin.MaxFrameBytes || capacity != plugin.MaxFrameBytes {
		return abi.Invalid
	}
	input := append([]byte(nil), unsafe.Slice((*byte)(request), int(requestLen))...)
	data, status := s.Call(handle, operation, input)
	if status != abi.OK {
		return status
	}
	copy(unsafe.Slice((*byte)(response), int(capacity)), data)
	*responseLen = uint32(len(data))
	return abi.OK
}
