// Package lib is lib/ledger's library interface in pure Go: the same objects opened from the same
// JSON configs, the same operations by name with JSON in and out, and the same code names, so a
// program drives the pure-Go ledger and the C one (bound by cengine/library.BindInterface) through one
// shape. Its Library and cengine's Interface both satisfy Interface.
//
// Here a store is the memory store, with its watches, and handle 0 answers canonical_transaction and
// code_name. A file, reader or SQLite store, a recorder, a supervisor and the collectors are the C
// library's for now and answer unsupported.
package lib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	ledger "github.com/webong/ext/pkg/ext-go/engine/ledger"
)

// Interface is the library interface every implementation of the ledger serves.
type Interface interface {
	Open(config []byte) (uint64, error)
	Call(handle uint64, op string, input []byte) ([]byte, error)
	Close(handle uint64) error
}

// ABIVersion is the interface version this package serves, EXT_LEDGER_ABI_VERSION in C.
const ABIVersion = 1

// A CodeError carries a contract code name ("invalid", "not_found", ...), as the C library returns.
type CodeError struct {
	Op   string
	Name string
	Err  error
}

func (e *CodeError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("ext_ledger %s: %s: %v", e.Op, e.Name, e.Err)
	}
	return fmt.Sprintf("ext_ledger %s: %s", e.Op, e.Name)
}

func (e *CodeError) Unwrap() error { return e.Err }

// CodeName is the code's name.
func (e *CodeError) CodeName() string { return e.Name }

// Code is the code name an error of either implementation carries: a CodeError's Name, or any error
// with a Name field of that meaning (cengine/library.CodeError), or ledger.Code's.
func Code(err error) string {
	if err == nil {
		return ""
	}
	var c *CodeError
	if errors.As(err, &c) {
		return c.Name
	}
	var named interface{ CodeName() string }
	if errors.As(err, &named) {
		return named.CodeName()
	}
	return ledger.Code(err)
}

type object struct {
	store    ledger.Store
	watch    <-chan ledger.Change
	cancel   context.CancelFunc // a watch's
	parent   *object
	children int
	inflight int
	closing  bool
}

// Library is one set of handles over pure-Go ledger objects.
type Library struct {
	mu      sync.Mutex
	objects map[uint64]*object
	next    uint64
}

// New returns an empty library.
func New() *Library { return &Library{objects: map[uint64]*object{}, next: 1} }

func codeErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return &CodeError{Op: op, Name: ledger.Code(err), Err: err}
}

func named(op, name string) error { return &CodeError{Op: op, Name: name} }

type config struct {
	Object      string          `json:"object"`
	Backend     string          `json:"backend"`
	Path        string          `json:"path"`
	Options     *ledger.Options `json:"options"`
	Store       uint64          `json:"store"`
	Namespace   string          `json:"namespace"`
	After       *int64          `json:"after"`
	Buffer      int             `json:"buffer"`
	TimeoutMS   *int64          `json:"timeout_ms"`
	Environment json.RawMessage `json:"environment"`
	Machine     bool            `json:"machine"`
}

func decode(op string, data []byte, v any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, v); err != nil {
		return &CodeError{Op: op, Name: "invalid", Err: err}
	}
	return nil
}

// Open opens an object from a JSON config, as ext_ledger_open does.
func (l *Library) Open(data []byte) (uint64, error) {
	var c config
	if err := decode("open", data, &c); err != nil {
		return 0, err
	}
	switch c.Object {
	case "store":
		switch c.Backend {
		case "", "memory":
			options := ledger.Options{}
			if c.Options != nil {
				options = *c.Options
			}
			return l.add(&object{store: ledger.NewMemoryWithOptions(options)}, nil), nil
		case "file", "reader", "sqlite":
			if c.Path == "" {
				return 0, named("open", "invalid")
			}
			return 0, named("open", "unsupported")
		}
		return 0, named("open", "invalid")
	case "watch":
		parent, err := l.acquire(c.Store, true)
		if err != nil {
			return 0, err
		}
		defer l.release(parent)
		if c.After != nil && *c.After < 0 {
			return 0, named("open", "invalid")
		}
		after := uint64(0)
		if c.After != nil {
			after = uint64(*c.After)
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := parent.store.Watch(ctx, c.Namespace, after, c.Buffer)
		if err != nil {
			cancel()
			return 0, codeErr("open", err)
		}
		return l.add(&object{watch: ch, cancel: cancel}, parent), nil
	case "recorder", "supervisor":
		parent, err := l.acquire(c.Store, true)
		if err != nil {
			return 0, err
		}
		l.release(parent)
		return 0, named("open", "unsupported")
	}
	return 0, named("open", "invalid")
}

func (l *Library) add(o *object, parent *object) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	o.parent = parent
	if parent != nil {
		parent.children++
	}
	id := l.next
	l.next++
	l.objects[id] = o
	return id
}

func (l *Library) acquire(handle uint64, store bool) (*object, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	o, ok := l.objects[handle]
	if handle == 0 || !ok || o.closing || (store && o.store == nil) {
		return nil, named("open", "not_found")
	}
	o.inflight++
	return o, nil
}

func (l *Library) release(o *object) {
	l.mu.Lock()
	o.inflight--
	free := o.inflight == 0 && o.closing
	l.mu.Unlock()
	if free {
		destroy(o)
	}
}

func destroy(o *object) {
	if o.store != nil {
		_ = o.store.Close()
	}
	if o.cancel != nil {
		o.cancel()
	}
}

// Close frees the object behind handle once no call holds it, as ext_ledger_close does.
func (l *Library) Close(handle uint64) error {
	l.mu.Lock()
	o, ok := l.objects[handle]
	if !ok || o.closing {
		l.mu.Unlock()
		return named("close", "not_found")
	}
	if o.children > 0 {
		l.mu.Unlock()
		return named("close", "busy")
	}
	o.closing = true
	delete(l.objects, handle)
	if o.parent != nil {
		o.parent.children--
	}
	free := o.inflight == 0
	l.mu.Unlock()
	if free {
		destroy(o)
	}
	return nil
}

type input struct {
	TimeoutMS   *int64              `json:"timeout_ms"`
	Schema      *ledger.Schema      `json:"schema"`
	Transaction *ledger.Transaction `json:"transaction"`
	Namespace   *string             `json:"namespace"`
	ID          *string             `json:"id"`
	Query       json.RawMessage     `json:"query"`
	Start       *string             `json:"start"`
	MaxDepth    int                 `json:"max_depth"`
	MaxVertices int                 `json:"max_vertices"`
	After       *int64              `json:"after"`
	Limit       int                 `json:"limit"`
	Code        *int                `json:"code"`
}

// Call runs op on handle (0 for the library) with a JSON object, as ext_ledger_call does.
func (l *Library) Call(handle uint64, op string, data []byte) ([]byte, error) {
	var in input
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] != '{' {
		return nil, named(op, "invalid")
	}
	if err := decode(op, data, &in); err != nil {
		return nil, err
	}
	ctx := context.Background()
	if in.TimeoutMS != nil {
		if *in.TimeoutMS < 0 {
			return nil, named(op, "invalid")
		}
		if handle != 0 || op != "next" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(*in.TimeoutMS)*time.Millisecond)
			defer cancel()
		}
	}
	if handle == 0 {
		return callLibrary(op, in)
	}
	l.mu.Lock()
	o, ok := l.objects[handle]
	if !ok || o.closing {
		l.mu.Unlock()
		return nil, named(op, "not_found")
	}
	o.inflight++
	l.mu.Unlock()
	defer l.release(o)
	if o.watch != nil {
		return callWatch(o, op, in)
	}
	return callStore(ctx, o.store, op, in)
}

func callLibrary(op string, in input) ([]byte, error) {
	switch op {
	case "canonical_transaction":
		if in.Transaction == nil {
			return nil, named(op, "invalid")
		}
		canonical, err := ledger.Canonical(*in.Transaction)
		if err != nil {
			return nil, codeErr(op, err)
		}
		fingerprint, err := ledger.Fingerprint(*in.Transaction)
		if err != nil {
			return nil, codeErr(op, err)
		}
		return json.Marshal(struct {
			Canonical   json.RawMessage `json:"canonical"`
			Fingerprint string          `json:"fingerprint"`
		}{canonical, fingerprint})
	case "code_name":
		if in.Code == nil {
			return nil, named(op, "invalid")
		}
		names := []string{"", "invalid", "not_found", "changed", "exists", "denied", "too_large", "expired", "unsupported", "unavailable", "busy", "canceled", "timeout", "io"}
		name := "unknown"
		if *in.Code >= 0 && *in.Code < len(names) {
			name = names[*in.Code]
		}
		return json.Marshal(name)
	case "collect", "collect_environment", "vocabulary":
		return nil, named(op, "unsupported")
	}
	return nil, named(op, "unsupported")
}

func callWatch(o *object, op string, in input) ([]byte, error) {
	if op != "next" {
		return nil, named(op, "unsupported")
	}
	timeout := time.Duration(0)
	if in.TimeoutMS != nil {
		timeout = time.Duration(*in.TimeoutMS) * time.Millisecond
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case change, ok := <-o.watch:
		if !ok {
			return nil, named(op, "unavailable")
		}
		return json.Marshal(change)
	case <-timer.C:
		return nil, named(op, "timeout")
	}
}

func callStore(ctx context.Context, s ledger.Store, op string, in input) ([]byte, error) {
	null := []byte("null")
	need := func(ok bool) error {
		if !ok {
			return named(op, "invalid")
		}
		return nil
	}
	answer := func(v any, err error) ([]byte, error) {
		if err != nil {
			return nil, codeErr(op, err)
		}
		return json.Marshal(v)
	}
	switch op {
	case "register":
		if err := need(in.Schema != nil); err != nil {
			return nil, err
		}
		if err := s.Register(ctx, *in.Schema); err != nil {
			return nil, codeErr(op, err)
		}
		return null, nil
	case "apply":
		if err := need(in.Transaction != nil); err != nil {
			return nil, err
		}
		return answer(s.Apply(ctx, *in.Transaction))
	case "get_vertex", "get_edge":
		if err := need(in.Namespace != nil && in.ID != nil); err != nil {
			return nil, err
		}
		if op == "get_vertex" {
			return answer(s.GetVertex(ctx, *in.Namespace, *in.ID))
		}
		return answer(s.GetEdge(ctx, *in.Namespace, *in.ID))
	case "query_vertices":
		var q ledger.VertexQuery
		if err := need(len(in.Query) > 0 && json.Unmarshal(in.Query, &q) == nil); err != nil {
			return nil, err
		}
		return answer(s.QueryVertices(ctx, q))
	case "query_edges":
		var q ledger.EdgeQuery
		if err := need(len(in.Query) > 0 && json.Unmarshal(in.Query, &q) == nil); err != nil {
			return nil, err
		}
		return answer(s.QueryEdges(ctx, q))
	case "traverse":
		if err := need(in.Namespace != nil && in.Start != nil); err != nil {
			return nil, err
		}
		return answer(s.Traverse(ctx, *in.Namespace, *in.Start, in.MaxDepth, in.MaxVertices))
	case "snapshot":
		if err := need(in.Namespace != nil); err != nil {
			return nil, err
		}
		return answer(s.Snapshot(ctx, *in.Namespace))
	case "changes":
		if err := need(in.After == nil || *in.After >= 0); err != nil {
			return nil, err
		}
		after := uint64(0)
		if in.After != nil {
			after = uint64(*in.After)
		}
		namespace := ""
		if in.Namespace != nil {
			namespace = *in.Namespace
		}
		return answer(s.Changes(ctx, namespace, after, in.Limit))
	}
	return nil, named(op, "unsupported")
}
