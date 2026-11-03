package wasm

import "errors"

var (
	// ErrInvalid reports invalid module bytes, options, or call data.
	ErrInvalid = errors.New("wasm: invalid")
	// ErrUnsupported reports a module shape the selected runner cannot host.
	ErrUnsupported = errors.New("wasm: unsupported")
	// ErrMismatch reports a reactor ABI version mismatch.
	ErrMismatch = errors.New("wasm: ABI mismatch")
	// ErrClosed reports use of a closed WebAssembly instance.
	ErrClosed = errors.New("wasm: closed")
)
