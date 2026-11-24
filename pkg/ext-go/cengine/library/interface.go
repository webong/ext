package library

import (
	"errors"
	"fmt"
	"unsafe"
)

// An Interface is a library's four-function interface, the one every library of lib/ exports
// beside its typed C API: <prefix>_abi_version, <prefix>_open (a JSON config in, a handle out),
// <prefix>_call (a handle, an operation name and a JSON object in, a JSON value out) and
// <prefix>_close, with <prefix>_free for what call returns and, when exported, <prefix>_code_name
// for its error codes. It knows nothing else about the library: ext_ledger's objects and operations
// are the ledger's, ext_plugin's the plugin engine's.
type Interface struct {
	prefix   string
	library  *Library
	version  func() uint32
	open     func(config *byte, length uintptr, handle *uint64) int32
	call     func(handle uint64, op *byte, input *byte, length uintptr, out **byte, outLength *uintptr) int32
	close    func(handle uint64) int32
	free     func(p unsafe.Pointer)
	codeName func(code int32) string
}

// A CodeError is a non-zero code a library returned, with its name when the library exports one.
type CodeError struct {
	Library string
	Op      string
	Code    int32
	Name    string
}

// CodeName is the library's name for the code, empty when it exports none.
func (e *CodeError) CodeName() string { return e.Name }

func (e *CodeError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("%s %s: %s", e.Library, e.Op, e.Name)
	}
	return fmt.Sprintf("%s %s: code %d", e.Library, e.Op, e.Code)
}

// BindInterface binds prefix's interface in lib and checks its ABI version is want.
func BindInterface(lib *Library, prefix string, want uint32) (*Interface, error) {
	if !libraryName.MatchString(prefix) {
		return nil, fmt.Errorf("library: prefix %q", prefix)
	}
	i := &Interface{prefix: prefix, library: lib}
	if err := lib.BindAll(map[string]any{
		prefix + "_abi_version": &i.version,
		prefix + "_open":        &i.open,
		prefix + "_call":        &i.call,
		prefix + "_close":       &i.close,
		prefix + "_free":        &i.free,
	}); err != nil {
		return nil, fmt.Errorf("library: %s does not export the library interface: %w", lib.Path(), err)
	}
	if _, err := lib.Symbol(prefix + "_code_name"); err == nil {
		if err := lib.Bind(&i.codeName, prefix+"_code_name"); err != nil {
			return nil, err
		}
	}
	if got := i.version(); got != want {
		return nil, fmt.Errorf("library: %s has %s ABI version %d, this binding speaks %d", lib.Path(), prefix, got, want)
	}
	return i, nil
}

// Prefix is the library's C prefix.
func (i *Interface) Prefix() string { return i.prefix }

// Open opens an object from a JSON config and returns its handle.
func (i *Interface) Open(config []byte) (uint64, error) {
	var handle uint64
	if code := i.open(bytePtr(config), uintptr(len(config)), &handle); code != 0 {
		return 0, i.err("open", code)
	}
	return handle, nil
}

// Call runs op on handle (0 for the library itself) with a JSON object and returns the JSON value
// it answers.
func (i *Interface) Call(handle uint64, op string, input []byte) ([]byte, error) {
	name, err := cString(op)
	if err != nil {
		return nil, err
	}
	var out *byte
	var length uintptr
	code := i.call(handle, &name[0], bytePtr(input), uintptr(len(input)), &out, &length)
	if out != nil {
		defer i.free(unsafe.Pointer(out))
	}
	if code != 0 {
		return nil, i.err(op, code)
	}
	if out == nil {
		return []byte("null"), nil
	}
	return append([]byte(nil), unsafe.Slice(out, int(length))...), nil
}

// Close frees the object behind handle.
func (i *Interface) Close(handle uint64) error {
	if code := i.close(handle); code != 0 {
		return i.err("close", code)
	}
	return nil
}

func (i *Interface) err(op string, code int32) error {
	e := &CodeError{Library: i.prefix, Op: op, Code: code}
	if i.codeName != nil {
		e.Name = i.codeName(code)
	}
	return e
}

func bytePtr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func cString(s string) ([]byte, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return nil, errors.New("library: an operation name holds NUL")
		}
	}
	return append([]byte(s), 0), nil
}
