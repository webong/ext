//go:build ext_cengine && cgo && (darwin || linux)

package goengine

/*
#include "ext_stream.h"
#include <stdlib.h>
ext_status ext_go_streams_create(uintptr_t,uint32_t,uint32_t,ext_streams **);
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/author"
	"github.com/webong/ext/pkg/plugin/stream"
	"runtime/cgo"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Streams supplies the ext.stream/v1 contract using the shared C lifecycle.
// Close must be called. Scope and reader callbacks must not reenter this service.
type Streams struct {
	mu         sync.RWMutex
	ptr        *C.ext_streams
	handle     cgo.Handle
	options    stream.Options
	life       context.Context
	cancel     context.CancelFunc
	errorsMu   sync.Mutex
	cleanupErr error
}
type streamValue struct {
	reader stream.Reader
	life   context.Context
	cancel context.CancelFunc
}
type streamRequestKey struct{}

func NewStreams(options stream.Options) (*Streams, error) {
	if options.Capacity < 1 || options.Capacity > 1024 || options.MaxAge <= 0 || options.MaxAge > 24*time.Hour || options.Scope == nil || options.Open == nil {
		return nil, plugin.ErrInvalid
	}
	life, cancel := context.WithCancel(context.Background())
	s := &Streams{options: options, life: life, cancel: cancel}
	s.handle = cgo.NewHandle(s)
	ms := options.MaxAge / time.Millisecond
	if ms == 0 {
		ms = 1
	}
	if err := status(C.ext_go_streams_create(C.uintptr_t(s.handle), C.uint32_t(options.Capacity), C.uint32_t(ms), &s.ptr)); err != nil {
		s.handle.Delete()
		cancel()
		return nil, err
	}
	return s, nil
}
func streamStatus(code C.ext_status) error {
	switch code {
	case C.EXT_DENIED:
		return &plugin.RemoteError{Code: "denied", Message: "stream access denied"}
	case C.EXT_CAPACITY:
		return &plugin.RemoteError{Code: "capacity", Message: "stream capacity reached"}
	case C.EXT_SEQUENCE:
		return &plugin.RemoteError{Code: "sequence", Message: "stream sequence mismatch"}
	}
	return status(code)
}
func (s *Streams) scope(ctx context.Context, r plugin.Request) ([]byte, error) {
	scope, err := s.options.Scope(ctx, r.Clone())
	if err != nil || scope == "" || len(scope) > 256 {
		return nil, streamStatus(C.EXT_DENIED)
	}
	return []byte(scope), nil
}
func (s *Streams) Register(r *author.Registry) error {
	if err := author.Register(r, stream.OpenMethod(), s.open); err != nil {
		return err
	}
	if err := author.Register(r, stream.ReadMethod(), s.read); err != nil {
		return err
	}
	return author.Register(r, stream.CloseMethod(), s.remove)
}
func (s *Streams) open(ctx context.Context, r plugin.Request, input stream.OpenRequest) (stream.OpenResponse, error) {
	var result stream.OpenResponse
	subject, err := s.scope(ctx, r)
	if err != nil {
		return result, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return result, plugin.ErrClosed
	}
	ctx = context.WithValue(ctx, streamRequestKey{}, r.Clone())
	scope, err := resourceScope(ctx)
	if err != nil {
		return result, err
	}
	defer scope.finish()
	var out C.ext_buffer
	code := C.ext_streams_open(s.ptr, bytesPointer(subject), C.size_t(len(subject)), bytesPointer(input.Parameters), C.size_t(len(input.Parameters)), &scope.options, &out)
	defer C.ext_buffer_free(&out)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err = scope.result(streamStatus(code)); err != nil {
		return result, err
	}
	err = json.Unmarshal(C.GoBytes(unsafe.Pointer(out.data), C.int(out.len)), &result.ID)
	return result, err
}
func (s *Streams) read(ctx context.Context, r plugin.Request, input stream.ReadRequest) (stream.Batch, error) {
	var result stream.Batch
	subject, err := s.scope(ctx, r)
	if err != nil {
		return result, err
	}
	if strings.IndexByte(input.ID, 0) >= 0 {
		return result, plugin.ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return result, streamStatus(C.EXT_DENIED)
	}
	scope, err := resourceScope(ctx)
	if err != nil {
		return result, err
	}
	defer scope.finish()
	id := C.CString(input.ID)
	defer C.free(unsafe.Pointer(id))
	var out C.ext_buffer
	code := C.ext_streams_read(s.ptr, bytesPointer(subject), C.size_t(len(subject)), id, C.uint64_t(input.Sequence), C.uint32_t(input.Limit), &scope.options, &out)
	defer C.ext_buffer_free(&out)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err = scope.result(streamStatus(code)); err != nil {
		return result, err
	}
	err = plugin.Decode(C.GoBytes(unsafe.Pointer(out.data), C.int(out.len)), &result)
	return result, err
}
func (s *Streams) remove(ctx context.Context, r plugin.Request, input stream.CloseRequest) (stream.Empty, error) {
	subject, err := s.scope(ctx, r)
	if err != nil {
		return stream.Empty{}, err
	}
	if strings.IndexByte(input.ID, 0) >= 0 {
		return stream.Empty{}, plugin.ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return stream.Empty{}, nil
	}
	id := C.CString(input.ID)
	defer C.free(unsafe.Pointer(id))
	return stream.Empty{}, streamStatus(C.ext_streams_remove(s.ptr, bytesPointer(subject), C.size_t(len(subject)), id))
}
func (s *Streams) Close() error {
	s.cancel()
	s.mu.RLock()
	if s.ptr != nil {
		C.ext_streams_close(s.ptr)
	}
	s.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptr != nil {
		if err := status(C.ext_streams_destroy(s.ptr)); err != nil {
			return err
		}
		s.ptr = nil
		s.handle.Delete()
	}
	s.errorsMu.Lock()
	defer s.errorsMu.Unlock()
	return s.cleanupErr
}

//export ctxGoStreamOpen
func ctxGoStreamOpen(handle, caller C.uintptr_t, data *C.uint8_t, n C.size_t, out *C.uintptr_t) (result C.int32_t) {
	result = C.EXT_IO
	defer func() { _ = recover() }()
	s := cgo.Handle(handle).Value().(*Streams)
	ctx := cgo.Handle(caller).Value().(context.Context)
	request := ctx.Value(streamRequestKey{}).(plugin.Request)
	life, cancel := context.WithTimeout(s.life, s.options.MaxAge)
	keep := false
	defer func() {
		if !keep {
			cancel()
		}
	}()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	reader, err := s.options.Open(life, request, C.GoBytes(unsafe.Pointer(data), C.int(n)))
	if reader != nil {
		*out = C.uintptr_t(cgo.NewHandle(&streamValue{reader, life, cancel}))
		keep = true
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = life.Err()
	}
	return backendStatus(recordCallbackError(ctx, err))
}

//export ctxGoStreamRead
func ctxGoStreamRead(handle, caller C.uintptr_t, ms, limit C.uint32_t, emit C.ext_emit, sink unsafe.Pointer) (result C.int32_t) {
	result = C.EXT_IO
	defer func() { _ = recover() }()
	v := cgo.Handle(handle).Value().(*streamValue)
	parent := cgo.Handle(caller).Value().(context.Context)
	ctx, cancel := context.WithTimeout(parent, time.Duration(ms)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(v.life, cancel)
	defer stop()
	if v.life.Err() != nil {
		cancel()
	}
	items, done, err := v.reader.Read(ctx, int(limit))
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return backendStatus(recordCallbackError(ctx, err))
	}
	return emitJSON(stream.Batch{Items: items, Done: done}, emit, sink)
}

//export ctxGoStreamClose
func ctxGoStreamClose(handle, value C.uintptr_t) (result C.int32_t) {
	result = C.EXT_IO
	s := cgo.Handle(handle).Value().(*Streams)
	v := cgo.Handle(value).Value().(*streamValue)
	v.cancel()
	var err error
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("stream cleanup panicked")
		}
		if err != nil {
			s.errorsMu.Lock()
			s.cleanupErr = errors.Join(s.cleanupErr, err)
			s.errorsMu.Unlock()
		}
		result = backendStatus(err)
	}()
	err = v.reader.Close()
	return backendStatus(err)
}

//export ctxGoStreamRelease
func ctxGoStreamRelease(value C.uintptr_t) {
	h := cgo.Handle(value)
	h.Value().(*streamValue).cancel()
	h.Delete()
}
