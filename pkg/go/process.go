//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

/*
#include "ctx_host.h"
#include <stdlib.h>
ctx_status ctx_go_create_process(const char *,const char *const *,size_t,const char *const *,size_t,const uint8_t *,size_t,uintptr_t,ctx_host **);
*/
import "C"
import (
	"encoding/json"
	"github.com/webong/ctx/pkg/plugin"
	"runtime/cgo"
	"strings"
	"unsafe"
)

// ProcessOptions launches a reviewed executable directly, without a shell.
// Environment is explicit and never inherits the embedding process environment.
type ProcessOptions struct {
	Executable             string
	Arguments, Environment []string
}

func cStrings(values []string) (**C.char, func(), error) {
	if len(values) == 0 {
		return nil, func() {}, nil
	}
	ptr := C.calloc(C.size_t(len(values)), C.size_t(unsafe.Sizeof(uintptr(0))))
	if ptr == nil {
		return nil, nil, status(C.CTX_NOMEM)
	}
	array := unsafe.Slice((**C.char)(ptr), len(values))
	cleanup := func() {
		for _, v := range array {
			C.free(unsafe.Pointer(v))
		}
		C.free(ptr)
	}
	for i, v := range values {
		if strings.IndexByte(v, 0) >= 0 {
			cleanup()
			return nil, nil, plugin.ErrInvalid
		}
		array[i] = C.CString(v)
		if array[i] == nil {
			cleanup()
			return nil, nil, status(C.CTX_NOMEM)
		}
	}
	return (**C.char)(ptr), cleanup, nil
}
func NewProcess(options ProcessOptions, d plugin.Descriptor, verify, authorize Policy) (*Host, error) {
	if verify == nil || authorize == nil || strings.IndexByte(options.Executable, 0) >= 0 || len(options.Arguments) > 256 || len(options.Environment) > 256 {
		return nil, plugin.ErrInvalid
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	path := C.CString(options.Executable)
	defer C.free(unsafe.Pointer(path))
	args, freeArgs, err := cStrings(options.Arguments)
	if err != nil {
		return nil, err
	}
	defer freeArgs()
	env, freeEnv, err := cStrings(options.Environment)
	if err != nil {
		return nil, err
	}
	defer freeEnv()
	h := &Host{policy: cgo.NewHandle(&policies{verify: verify, authorize: authorize}), descriptor: d.Clone()}
	code := C.ctx_go_create_process(path, args, C.size_t(len(options.Arguments)), env, C.size_t(len(options.Environment)), bytesPointer(data), C.size_t(len(data)), C.uintptr_t(h.policy), &h.ptr)
	if err = status(code); err != nil {
		h.policy.Delete()
		return nil, err
	}
	return h, nil
}
