// Package stream provides a pull-based, bounded stream contract over any CTX
// unary backend. Reads supply backpressure; no unbounded queue or replay exists.
package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/author"
)

const ContractName = "ctx.stream"

func Contract() plugin.ContractRef { return plugin.ContractRef{Name: ContractName, Version: "v1"} }

const MaxItems = 256
const MaxBatchBytes = 1 << 20

// Reader must honor ctx, return at most limit items, and keep allocation bounded.
// Close must unblock Read and be safe concurrently with it. EOF may accompany
// a final nonempty batch. Factory-created resources must not use an invocation
// context as their lifetime: the service supplies a separate bounded lifetime.
type Reader interface {
	Read(context.Context, int) ([]json.RawMessage, bool, error)
	Close() error
}
type Options struct {
	Capacity int
	MaxAge   time.Duration
	// Scope derives an authenticated subject/tenant key from host-owned context
	// or validated domain authority. A caller-controlled string alone is unsafe.
	Scope func(context.Context, plugin.Request) (string, error)
	Open  func(context.Context, plugin.Request, json.RawMessage) (Reader, error)
}
type entry struct {
	reader   Reader
	scope    string
	ctx      context.Context
	cancel   context.CancelFunc
	gate     chan struct{}
	seq      uint64
	once     sync.Once
	closeErr error
	timer    *time.Timer
}
type Service struct {
	mu       sync.Mutex
	opts     Options
	streams  map[string]*entry
	opening  int
	closed   bool
	lifetime context.Context
	cancel   context.CancelFunc
}

func New(opts Options) (*Service, error) {
	if opts.Capacity <= 0 || opts.Capacity > 1024 || opts.MaxAge <= 0 || opts.MaxAge > 24*time.Hour || opts.Scope == nil || opts.Open == nil {
		return nil, plugin.ErrInvalid
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{opts: opts, streams: map[string]*entry{}, lifetime: ctx, cancel: cancel}, nil
}

type OpenRequest struct {
	Parameters json.RawMessage `json:"parameters"`
}
type OpenResponse struct {
	ID string `json:"id"`
}
type ReadRequest struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Limit    int    `json:"limit"`
}
type Batch struct {
	Items []json.RawMessage `json:"items"`
	Done  bool              `json:"done"`
}
type CloseRequest struct {
	ID string `json:"id"`
}
type Empty struct{}

func OpenMethod() author.Method[OpenRequest, OpenResponse] {
	return author.Method[OpenRequest, OpenResponse]{Contract: Contract(), Operation: plugin.Operation{Name: "open"}, ValidateInput: func(r OpenRequest) error { var raw json.RawMessage; return plugin.Decode(r.Parameters, &raw) }}
}
func ReadMethod() author.Method[ReadRequest, Batch] {
	return author.Method[ReadRequest, Batch]{Contract: Contract(), Operation: plugin.Operation{Name: "read"}, ValidateInput: func(r ReadRequest) error {
		if r.ID == "" || len(r.ID) > 64 || r.Sequence == 0 || r.Limit < 1 || r.Limit > MaxItems {
			return plugin.ErrInvalid
		}
		return nil
	}, ValidateOutput: validateBatch}
}
func CloseMethod() author.Method[CloseRequest, Empty] {
	return author.Method[CloseRequest, Empty]{Contract: Contract(), Operation: plugin.Operation{Name: "close"}, ValidateInput: func(r CloseRequest) error {
		if r.ID == "" || len(r.ID) > 64 {
			return plugin.ErrInvalid
		}
		return nil
	}}
}
func validateBatch(b Batch) error {
	if len(b.Items) > MaxItems {
		return plugin.ErrInvalid
	}
	size := 0
	for _, item := range b.Items {
		size += len(item)
		if size > MaxBatchBytes {
			return plugin.ErrInvalid
		}
		var raw json.RawMessage
		if err := plugin.Decode(item, &raw); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Register(r *author.Registry) error {
	if err := author.Register(r, OpenMethod(), s.open); err != nil {
		return err
	}
	if err := author.Register(r, ReadMethod(), s.read); err != nil {
		return err
	}
	return author.Register(r, CloseMethod(), s.close)
}
func denied() error { return &plugin.RemoteError{Code: "denied", Message: "stream access denied"} }
func (s *Service) scope(ctx context.Context, r plugin.Request) (string, error) {
	scope, err := s.opts.Scope(ctx, r.Clone())
	if err != nil || scope == "" || len(scope) > 256 {
		return "", denied()
	}
	return scope, nil
}
func (s *Service) open(ctx context.Context, r plugin.Request, input OpenRequest) (OpenResponse, error) {
	scope, err := s.scope(ctx, r)
	if err != nil {
		return OpenResponse{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return OpenResponse{}, plugin.ErrClosed
	}
	if len(s.streams)+s.opening >= s.opts.Capacity {
		s.mu.Unlock()
		return OpenResponse{}, &plugin.RemoteError{Code: "capacity", Message: "stream capacity reached"}
	}
	s.opening++
	s.mu.Unlock()
	lifetime, cancel := context.WithTimeout(s.lifetime, s.opts.MaxAge)
	stop := context.AfterFunc(ctx, cancel)
	reader, err := s.opts.Open(lifetime, r, input.Parameters)
	stop()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = lifetime.Err()
	}
	if err == nil && reader == nil {
		err = plugin.ErrInvalid
	}
	var idBytes [24]byte
	if err == nil {
		_, err = rand.Read(idBytes[:])
	}
	id := hex.EncodeToString(idBytes[:])
	s.mu.Lock()
	s.opening--
	if s.closed && err == nil {
		err = plugin.ErrClosed
	}
	if err != nil {
		s.mu.Unlock()
		cancel()
		if reader != nil {
			_ = reader.Close()
		}
		return OpenResponse{}, err
	}
	e := &entry{reader: reader, scope: scope, ctx: lifetime, cancel: cancel, gate: make(chan struct{}, 1)}
	s.streams[id] = e
	remaining, _ := lifetime.Deadline()
	e.timer = time.AfterFunc(time.Until(remaining), func() { _ = s.remove(id, e) })
	s.mu.Unlock()
	return OpenResponse{ID: id}, nil
}
func (s *Service) find(ctx context.Context, r plugin.Request, id string) (*entry, error) {
	scope, err := s.scope(ctx, r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.streams[id]
	if e == nil || e.scope != scope {
		return nil, denied()
	}
	return e, nil
}
func (s *Service) read(ctx context.Context, r plugin.Request, input ReadRequest) (Batch, error) {
	e, err := s.find(ctx, r, input.ID)
	if err != nil {
		return Batch{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	select {
	case <-ctx.Done():
		return Batch{}, ctx.Err()
	case e.gate <- struct{}{}:
	}
	defer func() { <-e.gate }()
	if err := e.ctx.Err(); err != nil {
		return Batch{}, err
	}
	if input.Sequence != e.seq+1 {
		return Batch{}, &plugin.RemoteError{Code: "sequence", Message: "stream sequence mismatch"}
	}
	items, done, err := e.reader.Read(ctx, input.Limit)
	if err == nil {
		err = ctx.Err()
	}
	batch := Batch{Items: items, Done: done}
	if batch.Items == nil {
		batch.Items = []json.RawMessage{}
	}
	if err == nil {
		err = validateBatch(batch)
	}
	if err == nil && len(items) > input.Limit {
		err = plugin.ErrInvalid
	}
	if err != nil {
		_ = s.remove(input.ID, e)
		return Batch{}, err
	}
	for i, item := range batch.Items {
		batch.Items[i] = append(json.RawMessage(nil), item...)
	}
	e.seq++
	if done {
		if err := s.remove(input.ID, e); err != nil {
			return Batch{}, err
		}
	}
	return batch, nil
}
func (s *Service) close(ctx context.Context, r plugin.Request, input CloseRequest) (Empty, error) {
	scope, err := s.scope(ctx, r)
	if err != nil {
		return Empty{}, err
	}
	s.mu.Lock()
	e := s.streams[input.ID]
	s.mu.Unlock()
	if e == nil {
		return Empty{}, nil
	}
	if e.scope != scope {
		return Empty{}, denied()
	}
	return Empty{}, s.remove(input.ID, e)
}
func (s *Service) remove(id string, e *entry) error {
	s.mu.Lock()
	if s.streams[id] == e {
		delete(s.streams, id)
	}
	s.mu.Unlock()
	e.once.Do(func() {
		e.cancel()
		if e.timer != nil {
			e.timer.Stop()
		}
		e.closeErr = e.reader.Close()
	})
	return e.closeErr
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	entries := make(map[string]*entry, len(s.streams))
	for k, e := range s.streams {
		entries[k] = e
	}
	s.mu.Unlock()
	var err error
	for id, e := range entries {
		err = errors.Join(err, s.remove(id, e))
	}
	return err
}

// Client represents one ephemeral stream. Serialize Read and Close calls.
// A failed read must not be retried: items may already have been consumed.
type Client struct {
	caller   author.Caller
	id       string
	sequence uint64
	done     bool
}

func Open(ctx context.Context, caller author.Caller, parameters json.RawMessage) (*Client, error) {
	r, err := author.Call(ctx, caller, OpenMethod(), OpenRequest{Parameters: parameters})
	if err != nil {
		return nil, err
	}
	if r.ID == "" || len(r.ID) > 64 {
		return nil, plugin.ErrInvalid
	}
	return &Client{caller: caller, id: r.ID}, nil
}
func (c *Client) Read(ctx context.Context, limit int) (Batch, error) {
	if c.done {
		return Batch{Items: []json.RawMessage{}, Done: true}, nil
	}
	if limit < 1 || limit > MaxItems {
		return Batch{}, plugin.ErrInvalid
	}
	c.sequence++
	b, err := author.Call(ctx, c.caller, ReadMethod(), ReadRequest{ID: c.id, Sequence: c.sequence, Limit: limit})
	if err != nil || b.Done {
		c.done = true
	}
	return b, err
}
func (c *Client) Close(ctx context.Context) error {
	c.done = true
	_, err := author.Call(ctx, c.caller, CloseMethod(), CloseRequest{ID: c.id})
	return err
}
