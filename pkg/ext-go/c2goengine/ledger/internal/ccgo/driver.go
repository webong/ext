//go:build ext_ledger_c2go && (darwin || linux) && (amd64 || arm64)

package ccgo

import (
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"modernc.org/libc"
	"unsafe"
)

// Driver transports the described ledger ABI through translated C.
type Driver struct{}

func (Driver) Prefix() string { return "ext_ledger" }
func (Driver) ABIVersion() uint32 {
	t := libc.NewTLS()
	defer t.Close()
	return ext_ledger_abi_version(t)
}
func status(t *libc.TLS, op string, rc int32) error {
	if rc == 0 {
		return nil
	}
	return &library.CodeError{Library: "ext_ledger", Op: op, Code: rc, Name: libc.GoString(ext_ledger_code_name(t, rc))}
}
func (Driver) Open(config []byte) (uint64, error) {
	t := libc.NewTLS()
	defer t.Close()
	p, err := libc.CString(string(config))
	if err != nil {
		return 0, err
	}
	defer libc.Xfree(t, p)
	slot := libc.Xcalloc(t, 1, 8)
	if slot == 0 {
		return 0, &library.CodeError{Library: "ext_ledger", Name: "unavailable"}
	}
	defer libc.Xfree(t, slot)
	rc := ext_ledger_open(t, p, uint64(len(config)), slot)
	return *(*uint64)(unsafe.Pointer(slot)), status(t, "open", rc)
}
func (Driver) Call(h uint64, op string, input []byte) ([]byte, error) {
	t := libc.NewTLS()
	defer t.Close()
	name, err := libc.CString(op)
	if err != nil {
		return nil, err
	}
	defer libc.Xfree(t, name)
	p, err := libc.CString(string(input))
	if err != nil {
		return nil, err
	}
	defer libc.Xfree(t, p)
	slots := libc.Xcalloc(t, 2, 8)
	if slots == 0 {
		return nil, &library.CodeError{Library: "ext_ledger", Name: "unavailable"}
	}
	defer libc.Xfree(t, slots)
	rc := ext_ledger_call(t, h, name, p, uint64(len(input)), slots, slots+8)
	out := *(*uintptr)(unsafe.Pointer(slots))
	n := *(*uint64)(unsafe.Pointer(slots + 8))
	if out != 0 {
		defer ext_ledger_free(t, out)
	}
	if err := status(t, op, rc); err != nil {
		return nil, err
	}
	if out == 0 {
		return []byte("null"), nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(out)), int(n))...), nil
}
func (Driver) Close(h uint64) error {
	t := libc.NewTLS()
	defer t.Close()
	return status(t, "close", ext_ledger_close(t, h))
}
