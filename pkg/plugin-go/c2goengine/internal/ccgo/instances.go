//go:build (darwin && amd64) || (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ccgo

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"modernc.org/libc"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	instancepkg "github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
)

// Instances over the translated engine (lib/plugin/src/instances.c): the counterpart of the cgo
// binding's instances.go in pkg/plugin-go/cengine. The engine owns leases, replacement, capacity and
// cleanup; Go owns the values and what creating and disposing of them means.

type instanceValue struct {
	value   any
	dispose func() error
	mu      sync.Mutex
	err     error // what the disposer returned, kept so the releasing call can report it
}

func (v *instanceValue) disposeError() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

type instancesCore struct {
	disposed    uint64 // disposals that failed, to attribute a retiring instance's error to the call that retired it
	disposedErr error
	mu          sync.RWMutex
	ptr         uintptr
	handle      Handle
	life        context.Context
	cancel      context.CancelFunc
	validate    func(json.RawMessage) error
	create      func(context.Context, string, json.RawMessage) (any, func() error, error)
	observe     func(instancepkg.Event)
	errorsMu    sync.Mutex
	cleanupErr  error
}

// Instances binds typed Go resource factories to engine-owned leases, replacement, capacity and
// cleanup. Close frees the engine after all outstanding leases and factories finish. A timed-out
// Close can be retried.
type Instances[T any] struct{ core *instancesCore }

var (
	fInstanceValidate = instanceValidate
	fInstanceCreate   = instanceCreate
	fInstanceDispose  = instanceDispose
	fInstanceObserve  = instanceObserve
)

// NewInstances creates a manager of at most options.Capacity instances.
func NewInstances[T any](options instancepkg.Options[T]) (*Instances[T], error) {
	if options.Capacity < 1 || options.Capacity > 4096 || options.Validate == nil || options.Create == nil {
		return nil, plugin.ErrInvalid
	}
	life, cancel := context.WithCancel(context.Background())
	core := &instancesCore{life: life, cancel: cancel, validate: options.Validate, observe: options.Observe, create: func(ctx context.Context, k string, c json.RawMessage) (any, func() error, error) {
		return options.Create(ctx, k, c)
	}}
	core.handle = NewHandle(core)
	tls := acquire()
	defer release(tls)
	o := alloc(tls, unsafe.Sizeof(ext_instance_options{}))
	out := alloc(tls, unsafe.Sizeof(uintptr(0)))
	defer libc.Xfree(tls, o)
	defer libc.Xfree(tls, out)
	*(*ext_instance_options)(unsafe.Pointer(o)) = ext_instance_options{
		Fstruct_size: uint32_t(unsafe.Sizeof(ext_instance_options{})),
		Fcapacity:    uint32_t(options.Capacity),
		Fuser:        uintptr(core.handle),
		Fvalidate:    pointer(&fInstanceValidate),
		Fcreate:      pointer(&fInstanceCreate),
		Fdispose:     pointer(&fInstanceDispose),
		Fobserve:     pointer(&fInstanceObserve),
	}
	if err := statusError(ext_instances_create(tls, o, out)); err != nil {
		core.handle.Delete()
		cancel()
		return nil, err
	}
	core.ptr = *(*uintptr)(unsafe.Pointer(out))
	return &Instances[T]{core}, nil
}

func instanceStatus(s int32) error {
	switch s {
	case stUpdating:
		return instancepkg.ErrUpdating
	case stCapacity:
		return instancepkg.ErrCapacity
	}
	return statusError(s)
}

// Configure atomically replaces the instance under key, as instance.Manager.Configure does.
func (m *Instances[T]) Configure(ctx context.Context, key, revision string, config json.RawMessage) error {
	if key == "" || len(key) > 256 || revision == "" || len(revision) > 256 {
		return plugin.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c := m.core
	// Domain validation belongs to the binding. Run it against a detached copy before admission,
	// preserving its original Go error; the engine still owns strict JSON validation, capacity,
	// revisions and the factory lifecycle.
	config = append(json.RawMessage(nil), config...)
	if err := validateJSON(config); err != nil {
		return err
	}
	if err := c.validate(append(json.RawMessage(nil), config...)); err != nil {
		return err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ptr == 0 {
		return plugin.ErrClosed
	}
	scope, err := resourceScope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	before := c.disposeCount()
	tls := acquire()
	defer release(tls)
	k, freeKey := copyIn(tls, []byte(key))
	defer freeKey()
	r, freeRevision := copyIn(tls, []byte(revision))
	defer freeRevision()
	cfg, freeConfig := copyIn(tls, config)
	defer freeConfig()
	s := ext_instances_configure(tls, c.ptr, k, size_t(len(key)), r, size_t(len(revision)), cfg, size_t(len(config)), scope.native.options)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err = scope.result(instanceStatus(s))
	// Replacing an instance with no leases disposes the old one inside this call; the Go manager
	// returns what its disposer returned.
	if err != nil {
		if cause := c.disposeCauseSince(before); cause != nil {
			return cause
		}
	}
	return err
}

// validateJSON asks the engine whether data is one strict JSON document.
func validateJSON(data []byte) error {
	if len(data) == 0 {
		return plugin.ErrInvalid
	}
	tls := acquire()
	defer release(tls)
	in, free := copyIn(tls, data)
	defer free()
	return statusError(ext_host_validate_json(tls, in, size_t(len(data))))
}

// InstanceLease holds an instance until Release.
type InstanceLease[T any] struct {
	Value    T
	Revision string
	core     *instancesCore
	value    *instanceValue
	ptr      uintptr
	once     sync.Once
	err      error
}

// Acquire leases the instance under key.
func (m *Instances[T]) Acquire(key string) (*InstanceLease[T], error) {
	c := m.core
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ptr == 0 {
		return nil, plugin.ErrClosed
	}
	if len(key) == 0 {
		return nil, plugin.ErrNotFound
	}
	tls := acquire()
	defer release(tls)
	k, freeKey := copyIn(tls, []byte(key))
	defer freeKey()
	out := alloc(tls, unsafe.Sizeof(uintptr(0)))
	defer libc.Xfree(tls, out)
	if err := instanceStatus(ext_instances_acquire(tls, c.ptr, k, size_t(len(key)), out)); err != nil {
		return nil, err
	}
	lease := *(*uintptr)(unsafe.Pointer(out))
	v := Handle(ext_lease_value(tls, lease)).Value().(*instanceValue)
	n := alloc(tls, unsafe.Sizeof(size_t(0)))
	defer libc.Xfree(tls, n)
	revision := ext_lease_revision(tls, lease, n)
	var value T
	if v.value != nil {
		value = v.value.(T)
	}
	return &InstanceLease[T]{Value: value, Revision: string(bytesAt(revision, *(*size_t)(unsafe.Pointer(n)))), ptr: lease, core: c, value: v}, nil
}

// Release returns the lease; the one that drops the last reference disposes of a retired instance
// and reports what its disposer returned.
func (l *InstanceLease[T]) Release() error {
	l.once.Do(func() {
		l.core.mu.RLock()
		defer l.core.mu.RUnlock()
		tls := acquire()
		defer release(tls)
		l.err = instanceStatus(ext_lease_release(tls, l.ptr))
		if l.err != nil {
			if cause := l.value.disposeError(); cause != nil {
				l.err = cause
			}
		}
		l.ptr = 0
	})
	return l.err
}

func (c *instancesCore) disposeCount() uint64 {
	c.errorsMu.Lock()
	defer c.errorsMu.Unlock()
	return c.disposed
}

// disposeCauseSince is the most recent disposer error if one failed after the count.
func (c *instancesCore) disposeCauseSince(count uint64) error {
	c.errorsMu.Lock()
	defer c.errorsMu.Unlock()
	if c.disposed != count {
		return c.disposedErr
	}
	return nil
}

// Remove retires the instance under key.
func (m *Instances[T]) Remove(key string) error {
	c := m.core
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ptr == 0 {
		return plugin.ErrClosed
	}
	if len(key) == 0 {
		return plugin.ErrNotFound
	}
	before := c.disposeCount()
	tls := acquire()
	defer release(tls)
	k, freeKey := copyIn(tls, []byte(key))
	defer freeKey()
	err := instanceStatus(ext_instances_remove(tls, c.ptr, k, size_t(len(key))))
	if err != nil {
		if cause := c.disposeCauseSince(before); cause != nil {
			return cause
		}
	}
	return err
}

// Close stops admission and waits for leases and factories; a deadline leaves the manager closed and
// Close can be called again.
func (m *Instances[T]) Close(ctx context.Context) error {
	c := m.core
	c.cancel()
	scope, err := resourceScope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	c.mu.RLock()
	if c.ptr == 0 {
		c.mu.RUnlock()
		c.errorsMu.Lock()
		defer c.errorsMu.Unlock()
		return c.cleanupErr
	}
	tls := acquire()
	s := ext_instances_close(tls, c.ptr, scope.native.options)
	release(tls)
	c.mu.RUnlock()
	if s == stTimeout || s == stCanceled {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return instanceStatus(s)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr != 0 {
		tls := acquire()
		destroyed := instanceStatus(ext_instances_destroy(tls, c.ptr))
		release(tls)
		if destroyed != nil {
			return destroyed
		}
		c.ptr = 0
		c.handle.Delete()
	}
	c.errorsMu.Lock()
	defer c.errorsMu.Unlock()
	if c.cleanupErr != nil {
		return c.cleanupErr
	}
	return instanceStatus(s)
}

func coreOf(user uintptr) *instancesCore { c, _ := Handle(user).Value().(*instancesCore); return c }

// instanceValidate is a no-op: Go validates before it enters the engine, to keep its own error.
func instanceValidate(_ *libc.TLS, _, _ uintptr, _ size_t) int32 { return stOK }

func instanceCreate(_ *libc.TLS, user, options, _ uintptr, key uintptr, kn size_t, config uintptr, cn size_t, out uintptr) (result int32) {
	result = stIO
	defer func() { _ = recover() }()
	c := coreOf(user)
	o := (*ext_call_options)(unsafe.Pointer(options))
	parent := Handle(o.Fvalue).Value().(context.Context)
	ctx, cancel := context.WithTimeout(parent, time.Duration(o.Ftimeout_ms)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(c.life, cancel)
	defer stop()
	if c.life.Err() != nil {
		cancel()
	}
	value, dispose, err := c.create(ctx, string(bytesAt(key, kn)), bytesAt(config, cn))
	*(*uintptr)(unsafe.Pointer(out)) = uintptr(NewHandle(&instanceValue{value: value, dispose: dispose}))
	if err == nil {
		err = ctx.Err()
	}
	return backendStatus(recordCallbackError(ctx, err))
}

func instanceDispose(_ *libc.TLS, user, value uintptr) (result int32) {
	result = stIO
	c := coreOf(user)
	h := Handle(value)
	v := h.Value().(*instanceValue)
	defer h.Delete()
	var err error
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("instance cleanup panicked")
		}
		v.mu.Lock()
		v.err = err
		v.mu.Unlock()
		if err != nil {
			c.errorsMu.Lock()
			c.disposed++
			c.disposedErr = err
			if c.cleanupErr == nil {
				c.cleanupErr = err
			}
			c.errorsMu.Unlock()
		}
		result = backendStatus(err)
	}()
	if v.dispose != nil {
		err = v.dispose()
	}
	return backendStatus(err)
}

func instanceObserve(_ *libc.TLS, user, key uintptr, kn size_t, revision uintptr, rn size_t, state uintptr) {
	defer func() { _ = recover() }()
	if c := coreOf(user); c != nil && c.observe != nil {
		c.observe(instancepkg.Event{Key: string(bytesAt(key, kn)), Revision: string(bytesAt(revision, rn)), State: libc.GoString(state)})
	}
}
