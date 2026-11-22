// Command guest is the Go -buildmode=c-shared guest the cshared tests load:
// the conformance guest behind the C ABI, as tests/crosslang/testdata/go-shared.
package main

/*
#include <stdint.h>
*/
import "C"
import (
	"context"
	"github.com/webong/ext/pkg/ext-go/cguest/guest"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/plugintest"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/transport/inprocess"
	"unsafe"
)

var server = func() *guest.Server {
	s, err := guest.New(func(context.Context) (plugin.Backend, error) {
		g, err := plugintest.Guest()
		if err != nil {
			return nil, err
		}
		return inprocess.New(g)
	}, guest.Options{})
	if err != nil {
		panic(err)
	}
	return s
}()

//export ext_plugin_guest_abi_version
func ext_plugin_guest_abi_version() C.uint32_t { return 1 }

//export ext_plugin_guest_open
func ext_plugin_guest_open() C.uint64_t { return C.uint64_t(server.Open()) }

//export ext_plugin_guest_call
func ext_plugin_guest_call(id C.uint64_t, op C.uint32_t, input *C.uint8_t, n C.uint32_t, out *C.uint8_t, cap C.uint32_t, size *C.uint32_t) C.uint32_t {
	return C.uint32_t(server.CallInto(uint64(id), uint32(op), unsafe.Pointer(input), uint32(n), unsafe.Pointer(out), uint32(cap), (*uint32)(unsafe.Pointer(size))))
}

//export ext_plugin_guest_close
func ext_plugin_guest_close(id C.uint64_t) { server.Close(uint64(id)) }
func main()                                {}
