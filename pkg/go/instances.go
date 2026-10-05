//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

/*
#include "ctx_instance.h"
ctx_status ctx_go_instances_create(uintptr_t,uint32_t,ctx_instances **);
uintptr_t ctx_go_lease_value(ctx_lease *);
void ctx_go_call_init(ctx_call_options *,uint32_t,const ctx_cancel *,uintptr_t);
*/
import "C"
import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/webong/ctx/pkg/plugin"
	"github.com/webong/ctx/pkg/plugin/instance"
	"math"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

type instanceValue struct {
	value   any
	dispose func() error
}
type instancesCore struct {
	mu         sync.RWMutex
	ptr        *C.ctx_instances
	handle     cgo.Handle
	life       context.Context
	cancel     context.CancelFunc
	validate   func(json.RawMessage) error
	create     func(context.Context, string, json.RawMessage) (any, func() error, error)
	observe    func(instance.Event)
	errorsMu   sync.Mutex
	cleanupErr error
}

// Instances binds typed Go resource factories to C-owned leases, replacement,
// capacity and cleanup. Close frees the engine after all outstanding leases
// and factories finish. A timed-out Close can be retried.
type Instances[T any] struct{ core *instancesCore }

func NewInstances[T any](options instance.Options[T]) (*Instances[T], error) {
	if options.Capacity < 1 || options.Capacity > 4096 || options.Validate == nil || options.Create == nil {
		return nil, plugin.ErrInvalid
	}
	life, cancel := context.WithCancel(context.Background())
	core := &instancesCore{life: life, cancel: cancel, validate: options.Validate, observe: options.Observe, create: func(ctx context.Context, k string, c json.RawMessage) (any, func() error, error) {
		return options.Create(ctx, k, c)
	}}
	core.handle = cgo.NewHandle(core)
	if err := status(C.ctx_go_instances_create(C.uintptr_t(core.handle), C.uint32_t(options.Capacity), &core.ptr)); err != nil {
		core.handle.Delete()
		cancel()
		return nil, err
	}
	return &Instances[T]{core}, nil
}
func bytesPointer(b []byte) *C.uint8_t {
	if len(b) == 0 {
		return nil
	}
	return (*C.uint8_t)(unsafe.Pointer(&b[0]))
}
func instanceStatus(s C.ctx_status) error {
	switch s {
	case C.CTX_UPDATING:
		return instance.ErrUpdating
	case C.CTX_CAPACITY:
		return instance.ErrCapacity
	}
	return status(s)
}

// Native call scope: preserves the caller context and joins cancellation before
// freeing any C signal or Go handle. Unlike sessions it imposes no 30s default.
func resourceScope(ctx context.Context) (*callScope, error) {
	ctx = errorContext(ctx)
	scope := &callScope{ctx: ctx}
	if err := status(C.ctx_cancel_create(&scope.cancel)); err != nil {
		return nil, err
	}
	scope.value = cgo.NewHandle(ctx)
	duration := C.uint32_t(math.MaxUint32)
	if _, ok := ctx.Deadline(); ok {
		duration = timeout(ctx)
	}
	C.ctx_go_call_init(&scope.options, duration, scope.cancel, C.uintptr_t(scope.value))
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { C.ctx_cancel_signal(scope.cancel); close(done) })
	if ctx.Err() != nil {
		C.ctx_cancel_signal(scope.cancel)
	}
	scope.finish = func() {
		if !stop() {
			<-done
		}
		C.ctx_cancel_destroy(scope.cancel)
		scope.value.Delete()
	}
	return scope, nil
}
func (m *Instances[T]) Configure(ctx context.Context, key, revision string, config json.RawMessage) error {
	if key == "" || len(key) > 256 || revision == "" || len(revision) > 256 {
		return plugin.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c := m.core
	// Domain validation belongs to the binding. Run it against a detached copy
	// before admission, preserving its original Go error; C still owns strict
	// JSON validation, capacity, revisions and the factory lifecycle.
	config = append(json.RawMessage(nil), config...)
	if err := ValidateJSON(config); err != nil {
		return err
	}
	if err := c.validate(append(json.RawMessage(nil), config...)); err != nil {
		return err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ptr == nil {
		return plugin.ErrClosed
	}
	scope, err := resourceScope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	k, r := []byte(key), []byte(revision)
	s := C.ctx_instances_configure(c.ptr, bytesPointer(k), C.size_t(len(k)), bytesPointer(r), C.size_t(len(r)), bytesPointer(config), C.size_t(len(config)), &scope.options)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return scope.result(instanceStatus(s))
}

type InstanceLease[T any] struct {
	Value    T
	Revision string
	core     *instancesCore
	ptr      *C.ctx_lease
	once     sync.Once
	err      error
}

func (m *Instances[T]) Acquire(key string) (*InstanceLease[T], error) {
	c := m.core
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ptr == nil {
		return nil, plugin.ErrClosed
	}
	k := []byte(key)
	if len(k) == 0 {
		return nil, plugin.ErrNotFound
	}
	var ptr *C.ctx_lease
	if err := instanceStatus(C.ctx_instances_acquire(c.ptr, bytesPointer(k), C.size_t(len(k)), &ptr)); err != nil {
		return nil, err
	}
	v := cgo.Handle(C.ctx_go_lease_value(ptr)).Value().(*instanceValue)
	var n C.size_t
	revision := C.ctx_lease_revision(ptr, &n)
	var value T
	if v.value != nil {
		value = v.value.(T)
	}
	return &InstanceLease[T]{Value: value, Revision: string(C.GoBytes(unsafe.Pointer(revision), C.int(n))), ptr: ptr, core: c}, nil
}
func (l *InstanceLease[T]) Release() error {
	l.once.Do(func() {
		l.core.mu.RLock()
		defer l.core.mu.RUnlock()
		l.err = instanceStatus(C.ctx_lease_release(l.ptr))
		l.ptr = nil
	})
	return l.err
}
func (m *Instances[T]) Remove(key string) error {
	c := m.core
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ptr == nil {
		return plugin.ErrClosed
	}
	k := []byte(key)
	if len(k) == 0 {
		return plugin.ErrNotFound
	}
	return instanceStatus(C.ctx_instances_remove(c.ptr, bytesPointer(k), C.size_t(len(k))))
}
func (m *Instances[T]) Close(ctx context.Context) error {
	c := m.core
	c.cancel()
	scope, err := resourceScope(ctx)
	if err != nil {
		return err
	}
	defer scope.finish()
	c.mu.RLock()
	if c.ptr == nil {
		c.mu.RUnlock()
		c.errorsMu.Lock()
		defer c.errorsMu.Unlock()
		return c.cleanupErr
	}
	s := C.ctx_instances_close(c.ptr, &scope.options)
	c.mu.RUnlock()
	if s == C.CTX_TIMEOUT || s == C.CTX_CANCELED {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return instanceStatus(s)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr != nil {
		if err := instanceStatus(C.ctx_instances_destroy(c.ptr)); err != nil {
			return err
		}
		c.ptr = nil
		c.handle.Delete()
	}
	c.errorsMu.Lock()
	defer c.errorsMu.Unlock()
	if c.cleanupErr != nil {
		return c.cleanupErr
	}
	return instanceStatus(s)
}

//export ctxGoInstanceCreate
func ctxGoInstanceCreate(handle, caller C.uintptr_t, ms C.uint32_t, key *C.uint8_t, kn C.size_t, config *C.uint8_t, cn C.size_t, out *C.uintptr_t) (result C.int32_t) {
	result = C.CTX_IO
	defer func() { _ = recover() }()
	c := cgo.Handle(handle).Value().(*instancesCore)
	parent := cgo.Handle(caller).Value().(context.Context)
	ctx, cancel := context.WithTimeout(parent, time.Duration(ms)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(c.life, cancel)
	defer stop()
	if c.life.Err() != nil {
		cancel()
	}
	value, dispose, err := c.create(ctx, string(C.GoBytes(unsafe.Pointer(key), C.int(kn))), C.GoBytes(unsafe.Pointer(config), C.int(cn)))
	*out = C.uintptr_t(cgo.NewHandle(&instanceValue{value, dispose}))
	if err == nil {
		err = ctx.Err()
	}
	return backendStatus(recordCallbackError(ctx, err))
}

//export ctxGoInstanceDispose
func ctxGoInstanceDispose(handle, value C.uintptr_t) (result C.int32_t) {
	result = C.CTX_IO
	c := cgo.Handle(handle).Value().(*instancesCore)
	h := cgo.Handle(value)
	v := h.Value().(*instanceValue)
	defer h.Delete()
	var err error
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("instance cleanup panicked")
		}
		if err != nil {
			c.errorsMu.Lock()
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

//export ctxGoInstanceObserve
func ctxGoInstanceObserve(handle C.uintptr_t, key *C.uint8_t, kn C.size_t, revision *C.uint8_t, rn C.size_t, state *C.char) {
	defer func() { _ = recover() }()
	c := cgo.Handle(handle).Value().(*instancesCore)
	if c.observe != nil {
		c.observe(instance.Event{Key: string(C.GoBytes(unsafe.Pointer(key), C.int(kn))), Revision: string(C.GoBytes(unsafe.Pointer(revision), C.int(rn))), State: C.GoString(state)})
	}
}
