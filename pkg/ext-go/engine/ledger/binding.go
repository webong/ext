// Package ledger binds the canonical C ledger through its described library
// interface. Store and recorder operations execute in C; Go retains wire types.
package ledger

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

type Library struct{ api *library.Described }

// Bind binds an explicitly supplied native library. Loading and installation
// policy belong to the application; the binding never falls back to a Go engine.
func Bind(native *library.Library) (*Library, error) {
	if native == nil {
		return nil, errors.New("ledger: native library is required")
	}
	api, err := library.BindInterface(native, "ext_ledger", 1)
	if err != nil {
		return nil, err
	}
	d, err := library.Describe(api)
	if err != nil {
		return nil, err
	}
	return &Library{api: d}, nil
}
func call(ctx context.Context, o *library.Object, op string, in map[string]any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The synchronous ABI accepts deadlines. Cancellation is checked before
	// entry; an already-entered call is not interrupted by closing a shared store.
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		in["timeout_ms"] = (remaining + time.Millisecond - 1) / time.Millisecond
	}
	err := o.Call(op, in, out)
	var code *library.CodeError
	if errors.As(err, &code) {
		switch code.Name {
		case "canceled":
			return context.Canceled
		case "timeout":
			return context.DeadlineExceeded
		}
	}
	return err
}
func (l *Library) Collect(ctx context.Context, what string, options map[string]any, out any) error {
	in := map[string]any{"what": what}
	for k, v := range options {
		in[k] = v
	}
	return call(ctx, l.api.Library(), "collect", in, out)
}
func (l *Library) OpenStore(ctx context.Context, backend, path string, options Options) (*NativeStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o, err := l.api.Open("store", map[string]any{"backend": backend, "path": path, "options": options}, nil)
	if err != nil {
		return nil, err
	}
	return &NativeStore{object: o, library: l}, nil
}

type NativeStore struct {
	mu      sync.Mutex
	closed  bool
	object  *library.Object
	library *Library
}

func (s *NativeStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	err := s.object.Close()
	if err == nil {
		s.closed = true
	}
	return err
}
func (s *NativeStore) call(ctx context.Context, op string, in map[string]any, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return call(ctx, s.object, op, in, out)
}
func (s *NativeStore) Register(ctx context.Context, schema Schema) error {
	return s.call(ctx, "register", map[string]any{"schema": schema}, nil)
}
func (s *NativeStore) Apply(ctx context.Context, tx Transaction) (out Commit, err error) {
	err = s.call(ctx, "apply", map[string]any{"transaction": tx}, &out)
	return
}
func (s *NativeStore) Snapshot(ctx context.Context, ns string) (out Snapshot, err error) {
	err = s.call(ctx, "snapshot", map[string]any{"namespace": ns}, &out)
	return
}
func (s *NativeStore) GetVertex(ctx context.Context, ns, id string) (out Vertex, err error) {
	err = s.call(ctx, "get_vertex", map[string]any{"namespace": ns, "id": id}, &out)
	return
}
func (s *NativeStore) GetEdge(ctx context.Context, ns, id string) (out Edge, err error) {
	err = s.call(ctx, "get_edge", map[string]any{"namespace": ns, "id": id}, &out)
	return
}
func (s *NativeStore) QueryVertices(ctx context.Context, q VertexQuery) (out []Vertex, err error) {
	err = s.call(ctx, "query_vertices", map[string]any{"query": q}, &out)
	return
}
func (s *NativeStore) QueryEdges(ctx context.Context, q EdgeQuery) (out []Edge, err error) {
	err = s.call(ctx, "query_edges", map[string]any{"query": q}, &out)
	return
}
func (s *NativeStore) Changes(ctx context.Context, ns string, after uint64, limit int) (out []Change, err error) {
	err = s.call(ctx, "changes", map[string]any{"namespace": ns, "after": after, "limit": limit}, &out)
	return
}

// Objects exposes the complete described C API, including watches and
// supervisors, without requiring a separate hand-maintained wrapper per method.
func (l *Library) Objects() *library.Described { return l.api }

// Reconcile replaces the complete desired namespace at its observed revision.
func (s *NativeStore) Reconcile(ctx context.Context, desired Snapshot) (out Commit, err error) {
	vertices, edges := desired.Vertices, desired.Edges
	if vertices == nil {
		vertices = []Vertex{}
	}
	if edges == nil {
		edges = []Edge{}
	}
	err = s.call(ctx, "reconcile", map[string]any{"transaction": map[string]any{"namespace": desired.Namespace, "expected_revision": desired.Revision, "vertices": vertices, "edges": edges}}, &out)
	return
}
