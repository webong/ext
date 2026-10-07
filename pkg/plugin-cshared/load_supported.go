//go:build cgo && (linux || darwin || freebsd || windows)

package cshared

/*
#cgo linux LDFLAGS: -ldl
#cgo freebsd LDFLAGS: -ldl
#include <stdlib.h>
#include "loader.h"
*/
import "C"

import (
	"fmt"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin-cshared/abi"
)

func Supported() bool { return true }

// The OS image and symbol table are deliberately pinned. Once per canonical
// path also prevents refcount growth when hosts reconnect to the same artifact.
var images sync.Map

type image struct {
	once    sync.Once
	library *C.ctx_library
	err     error
}
type session struct {
	library *C.ctx_library
	handle  C.uint64_t
}

func openNative(path string) (nativeSession, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	value, _ := images.LoadOrStore(canonical, &image{})
	i := value.(*image)
	i.once.Do(func() {
		name := C.CString(canonical)
		defer C.free(unsafe.Pointer(name))
		var message [1024]C.char
		i.library = C.ctx_library_load(name, &message[0], C.size_t(len(message)))
		if i.library == nil {
			i.err = fmt.Errorf("%w: load C shared plugin: %s", plugin.ErrUnsupported, C.GoString(&message[0]))
		}
	})
	if i.err != nil {
		return nil, i.err
	}
	handle := C.ctx_library_open(i.library)
	if handle == 0 {
		return nil, fmt.Errorf("%w: C shared guest refused a new handle", plugin.ErrDenied)
	}
	return &session{library: i.library, handle: handle}, nil
}

func (s *session) call(op uint32, input []byte) ([]byte, error) {
	// All memory crossing this boundary is C-owned. Native code cannot retain
	// these pointers and no Go pointers or allocator ownership cross runtimes.
	request := C.CBytes(input)
	defer C.free(request)
	response := C.malloc(C.size_t(plugin.MaxFrameBytes))
	if response == nil {
		return nil, fmt.Errorf("allocate C plugin response")
	}
	defer C.free(response)
	var size C.uint32_t
	status := C.ctx_library_call(s.library, s.handle, C.uint32_t(op), (*C.uint8_t)(request), C.uint32_t(len(input)), (*C.uint8_t)(response), C.uint32_t(plugin.MaxFrameBytes), &size)
	if uint32(status) != abi.OK {
		return nil, fmt.Errorf("%w: C ABI status %d", plugin.ErrInvalid, uint32(status))
	}
	if size == 0 || size > C.uint32_t(plugin.MaxFrameBytes) {
		return nil, plugin.ErrInvalid
	}
	return C.GoBytes(response, C.int(size)), nil
}
func (s *session) close() { C.ctx_library_close(s.library, s.handle) }
