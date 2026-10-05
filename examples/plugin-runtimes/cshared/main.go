//go:build cgo

// Build with -buildmode=c-shared. cgo generates the corresponding C header.
package main

/*
#include <stdint.h>
*/
import "C"

import (
	"github.com/webong/ctx/examples/plugin-runtimes/echo"
	"github.com/webong/ctx/pkg/go/cshared/guest"
	"github.com/webong/ctx/pkg/plugin/cshared/abi"
	"unsafe"
)

var server = newServer()

func newServer() *guest.Server {
	s, err := guest.New(echo.New, guest.Options{})
	if err != nil {
		panic(err)
	}
	return s
}

//export ctx_plugin_abi_version
func ctx_plugin_abi_version() C.uint32_t { return C.uint32_t(abi.Version) }

//export ctx_plugin_open
func ctx_plugin_open() C.uint64_t { return C.uint64_t(server.Open()) }

//export ctx_plugin_call
func ctx_plugin_call(handle C.uint64_t, op C.uint32_t, request *C.uint8_t, requestLen C.uint32_t, response *C.uint8_t, capacity C.uint32_t, responseLen *C.uint32_t) C.uint32_t {
	return C.uint32_t(server.CallInto(uint64(handle), uint32(op), unsafe.Pointer(request), uint32(requestLen), unsafe.Pointer(response), uint32(capacity), (*uint32)(unsafe.Pointer(responseLen))))
}

//export ctx_plugin_close
func ctx_plugin_close(handle C.uint64_t) { server.Close(uint64(handle)) }

func main() {}
