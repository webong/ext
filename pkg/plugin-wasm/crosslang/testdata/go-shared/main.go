package main

/*
#include <stdint.h>
*/
import "C"
import (
	"context"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin-go/cshared/guest"
	"github.com/webong/ext/pkg/plugin/inprocess"
	"github.com/webong/ext/pkg/plugin/plugintest"
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

//export ext_plugin_abi_version
func ext_plugin_abi_version() C.uint32_t { return 1 }

//export ext_plugin_open
func ext_plugin_open() C.uint64_t { return C.uint64_t(server.Open()) }

//export ext_plugin_call
func ext_plugin_call(id C.uint64_t, op C.uint32_t, input *C.uint8_t, n C.uint32_t, out *C.uint8_t, cap C.uint32_t, size *C.uint32_t) C.uint32_t {
	return C.uint32_t(server.CallInto(uint64(id), uint32(op), unsafe.Pointer(input), uint32(n), unsafe.Pointer(out), uint32(cap), (*uint32)(unsafe.Pointer(size))))
}

//export ext_plugin_close
func ext_plugin_close(id C.uint64_t) { server.Close(uint64(id)) }
func main()                          {}
