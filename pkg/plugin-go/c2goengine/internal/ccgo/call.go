//go:build (darwin && amd64) || (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ccgo

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"modernc.org/libc"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/endpoint"
)

// Available is true where this package holds the translation.
const Available = true

// Status codes of lib/plugin/include/ext_host.h.
const (
	stOK = iota
	stInvalid
	stDenied
	stMismatch
	stUnsupported
	stClosed
	stTimeout
	stIO
	stNoMem
	stDraining
	stNotFound
	stAmbiguous
	stCanceled
	stUpdating
	stCapacity
	stSequence
	stProtocol
	stExpired
)

// statusError names a status as the cgo binding does (pkg/plugin-go/cengine status).
func statusError(s int32) error {
	switch s {
	case stOK:
		return nil
	case stInvalid:
		return plugin.ErrInvalid
	case stDenied:
		return plugin.ErrDenied
	case stMismatch:
		return plugin.ErrMismatch
	case stUnsupported:
		return plugin.ErrUnsupported
	case stNotFound:
		return plugin.ErrNotFound
	case stAmbiguous:
		return plugin.ErrAmbiguous
	case stDraining:
		return plugin.ErrDraining
	case stClosed:
		return plugin.ErrClosed
	case stProtocol:
		return plugin.ErrProtocol
	case stExpired:
		return endpoint.ErrExpired
	case stCanceled:
		return context.Canceled
	case stTimeout:
		return context.DeadlineExceeded
	}
	return fmt.Errorf("C host status %d", s)
}

// Threads of the translated code need a libc thread-local state. A few are kept for reuse; calls
// beyond that make one and close it.
var states = make(chan *libc.TLS, 64)

func acquire() *libc.TLS {
	select {
	case tls := <-states:
		return tls
	default:
		return libc.NewTLS()
	}
}

func release(tls *libc.TLS) {
	select {
	case states <- tls:
	default:
		tls.Close()
	}
}

// Call runs one of the engine's pure services, as ext_engine_call does in C: bounded JSON in and
// out, no plugin executed, no policy inferred (lib/plugin/services.md).
func Call(operation string, input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, plugin.ErrInvalid
	}
	for _, c := range operation {
		if c == 0 {
			return nil, plugin.ErrInvalid
		}
	}
	tls := acquire()
	defer release(tls)
	name, err := libc.CString(operation)
	if err != nil {
		return nil, err
	}
	defer libc.Xfree(tls, name)
	in := libc.Xmalloc(tls, libc.Tsize_t(len(input)))
	if in == 0 {
		return nil, errors.New("c2goengine: out of memory")
	}
	defer libc.Xfree(tls, in)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(in)), len(input)), input)
	out := libc.Xcalloc(tls, 1, libc.Tsize_t(unsafe.Sizeof(ext_buffer{})))
	if out == 0 {
		return nil, errors.New("c2goengine: out of memory")
	}
	defer libc.Xfree(tls, out)
	s := ext_engine_call(tls, name, in, size_t(len(input)), out)
	buffer := (*ext_buffer)(unsafe.Pointer(out))
	defer ext_buffer_free(tls, out)
	if err := statusError(s); err != nil {
		return nil, err
	}
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(buffer.Fdata)), int(buffer.Flen1))...), nil
}
