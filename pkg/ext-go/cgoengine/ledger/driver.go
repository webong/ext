//go:build cgo && ext_ledger_cgo

// Package ledger binds the canonical ledger through a linked C ABI.
// Supply fetched headers/libraries using CGO_CFLAGS and CGO_LDFLAGS.
package ledger

/*
#include <stdlib.h>
#include <ext_ledger.h>
*/
import "C"

import (
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	model "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"unsafe"
)

type driver struct{}

func (driver) Prefix() string     { return "ext_ledger" }
func (driver) ABIVersion() uint32 { return uint32(C.ext_ledger_abi_version()) }
func code(op string, rc C.int) error {
	if rc == 0 {
		return nil
	}
	return &library.CodeError{Library: "ext_ledger", Op: op, Code: int32(rc), Name: C.GoString(C.ext_ledger_code_name(rc))}
}
func (driver) Open(config []byte) (uint64, error) {
	p := C.CBytes(config)
	defer C.free(p)
	var h C.uint64_t
	err := code("open", C.ext_ledger_open((*C.char)(p), C.size_t(len(config)), &h))
	return uint64(h), err
}
func (driver) Call(h uint64, op string, input []byte) ([]byte, error) {
	name := C.CString(op)
	defer C.free(unsafe.Pointer(name))
	p := C.CBytes(input)
	defer C.free(p)
	var out *C.char
	var n C.size_t
	rc := C.ext_ledger_call(C.uint64_t(h), name, (*C.char)(p), C.size_t(len(input)), &out, &n)
	if out != nil {
		defer C.ext_ledger_free(unsafe.Pointer(out))
	}
	if err := code(op, rc); err != nil {
		return nil, err
	}
	if out == nil {
		return []byte("null"), nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(out)), int(n))...), nil
}
func (driver) Close(h uint64) error { return code("close", C.ext_ledger_close(C.uint64_t(h))) }

// Bind uses only the linked C driver, never an implicit pure-Go fallback.
func Bind() (*model.Library, error) {
	i, err := library.FromDriver(driver{}, "ext_ledger", 1)
	if err != nil {
		return nil, err
	}
	return model.BindInterface(i)
}
