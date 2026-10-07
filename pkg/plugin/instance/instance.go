// Package instance manages configured resources independently of processes and
// connections. Consumers supply a key including tenant/subject scope as needed.
package instance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"

	"github.com/webong/ext/pkg/plugin"
)

var ErrUpdating = errors.New("plugin instance update in progress")
var ErrCapacity = errors.New("plugin instance capacity reached")

type Factory[T any] func(context.Context, string, json.RawMessage) (T, func() error, error)

// Event reports lifecycle metadata without configuration values.
type Event struct {
	Key      string
	Revision string
	State    string
}

type Options[T any] struct {
	// Observe must be fast and safe for concurrent calls.
	Observe  func(Event)
	Capacity int
	Validate func(json.RawMessage) error
	Create   Factory[T]
}

type entry[T any] struct {
	value    T
	key      string
	revision string
	digest   [32]byte
	dispose  func() error
	leases   int
	retired  bool
}
type Manager[T any] struct {
	mu         sync.Mutex
	opts       Options[T]
	entries    map[string]*entry[T]
	updating   map[string]bool
	closed     bool
	active     int // includes factories, live entries and retiring entries
	idle       chan struct{}
	cleanupErr error
	lifetime   context.Context
	cancel     context.CancelFunc
}

func New[T any](opts Options[T]) (*Manager[T], error) {
	if opts.Capacity <= 0 || opts.Capacity > 4096 || opts.Validate == nil || opts.Create == nil {
		return nil, plugin.ErrInvalid
	}
	idle := make(chan struct{})
	close(idle)
	lifetime, cancel := context.WithCancel(context.Background())
	return &Manager[T]{opts: opts, entries: map[string]*entry[T]{}, updating: map[string]bool{}, idle: idle, lifetime: lifetime, cancel: cancel}, nil
}
func (m *Manager[T]) observe(e Event) {
	if m.opts.Observe != nil {
		m.opts.Observe(e)
	}
}

func (m *Manager[T]) add() {
	if m.active == 0 {
		m.idle = make(chan struct{})
	}
	m.active++
}
func (m *Manager[T]) finish(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cleanupErr == nil && err != nil {
		m.cleanupErr = err
	}
	m.active--
	if m.active == 0 {
		close(m.idle)
	}
}
func (m *Manager[T]) dispose(e *entry[T]) error {
	var err error
	if e.dispose != nil {
		err = e.dispose()
	}
	m.finish(err)
	m.observe(Event{Key: e.key, Revision: e.revision, State: "disposed"})
	return err
}

// Configure atomically replaces an instance only after successful validation
// and creation. Revision must change with configuration. Existing leases keep
// the old instance alive until released. Factories must honor ctx; callbacks
// are called outside the manager lock. Capacity includes retiring instances.
func (m *Manager[T]) Configure(ctx context.Context, key, revision string, config json.RawMessage) error {
	if key == "" || len(key) > 256 || revision == "" || len(revision) > 256 {
		return plugin.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	config = append(json.RawMessage(nil), config...)
	var raw json.RawMessage
	if err := plugin.Decode(config, &raw); err != nil {
		return err
	}
	if err := m.opts.Validate(append(json.RawMessage(nil), config...)); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return plugin.ErrClosed
	}
	if m.updating[key] {
		m.mu.Unlock()
		return ErrUpdating
	}
	digest := sha256.Sum256(config)
	old := m.entries[key]
	if old != nil && old.revision == revision {
		m.mu.Unlock()
		if old.digest != digest {
			return plugin.ErrMismatch
		}
		return nil
	}
	if m.active >= m.opts.Capacity {
		m.mu.Unlock()
		return ErrCapacity
	}
	m.updating[key] = true
	m.add()
	m.mu.Unlock()
	factoryContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.lifetime, cancel)
	if m.lifetime.Err() != nil {
		cancel()
	}
	value, dispose, err := m.opts.Create(factoryContext, key, config)
	if err == nil {
		err = factoryContext.Err()
	}
	stop()
	cancel()
	m.mu.Lock()
	delete(m.updating, key)
	if m.closed && err == nil {
		err = plugin.ErrClosed
	}
	if err != nil {
		m.mu.Unlock()
		var cleanup error
		if dispose != nil {
			cleanup = dispose()
		}
		m.finish(cleanup)
		return errors.Join(err, cleanup)
	}
	e := &entry[T]{value: value, key: key, revision: revision, digest: digest, dispose: dispose}
	m.entries[key] = e
	cleanupOld := false
	if old != nil {
		old.retired = true
		cleanupOld = old.leases == 0
	}
	m.mu.Unlock()
	m.observe(Event{Key: key, Revision: revision, State: "configured"})
	if old != nil {
		m.observe(Event{Key: key, Revision: old.revision, State: "retired"})
	}
	if cleanupOld {
		return m.dispose(old)
	}
	return nil
}

type Lease[T any] struct {
	Value    T
	Revision string
	release  func() error
	once     sync.Once
	err      error
}

func (l *Lease[T]) Release() error { l.once.Do(func() { l.err = l.release() }); return l.err }
func (m *Manager[T]) Acquire(key string) (*Lease[T], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, plugin.ErrClosed
	}
	e := m.entries[key]
	if e == nil {
		return nil, plugin.ErrNotFound
	}
	e.leases++
	return &Lease[T]{Value: e.value, Revision: e.revision, release: func() error {
		m.mu.Lock()
		e.leases--
		cleanup := e.retired && e.leases == 0
		m.mu.Unlock()
		if cleanup {
			return m.dispose(e)
		}
		return nil
	}}, nil
}
func (m *Manager[T]) Remove(key string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return plugin.ErrClosed
	}
	if m.updating[key] {
		m.mu.Unlock()
		return ErrUpdating
	}
	e := m.entries[key]
	if e == nil {
		m.mu.Unlock()
		return plugin.ErrNotFound
	}
	delete(m.entries, key)
	e.retired = true
	cleanup := e.leases == 0
	m.mu.Unlock()
	if cleanup {
		return m.dispose(e)
	}
	return nil
}

// Close stops admission and waits for leases/factories/disposals. A deadline
// leaves the manager closed; callers can wait again. Resources are never
// destroyed while leased. Cleanup callbacks must be bounded by their owner.
func (m *Manager[T]) Close(ctx context.Context) error {
	m.mu.Lock()
	var cleanup []*entry[T]
	if !m.closed {
		m.closed = true
		m.cancel()
		for key, e := range m.entries {
			delete(m.entries, key)
			e.retired = true
			if e.leases == 0 {
				cleanup = append(cleanup, e)
			}
		}
	}
	idle := m.idle
	m.mu.Unlock()
	for _, e := range cleanup {
		go func(e *entry[T]) { _ = m.dispose(e) }(e)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-idle:
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.cleanupErr
	}
}
