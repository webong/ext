//go:build !cgo && (darwin || linux || windows) && (amd64 || arm64)

package cguest

// The cgo-free loader. The C ABI (lib/plugin/include/ext_plugin_guest.h) is four
// exports the host calls and no function pointer the guest calls back, so
// purego's limits on callbacks (NewCallback's fixed count, no callbacks on
// some platforms) never apply: this loader registers no callback at all.

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/webong/ext/pkg/ext-go/cengine/cguest/abi"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

func Supported() bool { return true }

var errGoGuestDarwinAMD64 = errors.New("a Go c-shared guest cannot run in a Go host on darwin/amd64: both Go runtimes keep g in the same fixed TLS slot; use a C, Rust or Zig guest, or a process guest")

// The OS image and symbol table are deliberately pinned, as in the cgo loader:
// once per canonical path, never unloaded, even after an ABI failure.
var images sync.Map

type image struct {
	once    sync.Once
	exports *exports
	err     error
}

type exports struct {
	open  func() uint64
	call  func(handle uint64, op uint32, input *byte, inputLen uint32, output *byte, outputCap uint32, outputLen *uint32) uint32
	close func(handle uint64)
}

type session struct {
	exports *exports
	handle  uint64
}

// responses reuses the MaxFrameBytes buffers the guest writes into. A buffer
// returns to the pool only after the native call that holds it has returned.
var responses = sync.Pool{New: func() any { b := make([]byte, abi.MaxFrameBytes); return &b }}

func openNative(path string) (nativeSession, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	value, _ := images.LoadOrStore(canonical, &image{})
	i := value.(*image)
	i.once.Do(func() {
		i.exports, err = load(canonical)
		if err != nil {
			i.err = fmt.Errorf("%w: load C shared plugin: %v", ErrUnsupported, err)
		}
	})
	if i.err != nil {
		return nil, i.err
	}
	handle := i.exports.open()
	if handle == 0 {
		return nil, fmt.Errorf("%w: C shared guest refused a new handle", ErrDenied)
	}
	return &session{exports: i.exports, handle: handle}, nil
}

func load(path string) (*exports, error) {
	// Native initialization has run once the image is mapped; it stays mapped.
	lib, err := library.Open(path)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"ext_plugin_guest_abi_version", "ext_plugin_guest_open", "ext_plugin_guest_call", "ext_plugin_guest_close"} {
		if _, err := lib.Symbol(name); err != nil {
			return nil, fmt.Errorf("missing required CTX C ABI exports")
		}
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		// Every Go c-shared library exports this. On darwin/amd64 each Go runtime
		// keeps its g in the same fixed TLS slot, so a Go guest called from a Go
		// host thread takes the host's g for its own and faults (with cgo too).
		if _, err := lib.Symbol("_cgo_wait_runtime_init_done"); err == nil {
			return nil, errGoGuestDarwinAMD64
		}
	}
	var version func() uint32
	if err := lib.Bind(&version, "ext_plugin_guest_abi_version"); err != nil {
		return nil, err
	}
	if version() != abi.Version {
		return nil, fmt.Errorf("unsupported CTX C ABI version")
	}
	e := &exports{}
	if err := lib.BindAll(map[string]any{"ext_plugin_guest_open": &e.open, "ext_plugin_guest_call": &e.call, "ext_plugin_guest_close": &e.close}); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *session) call(op uint32, input []byte) ([]byte, error) {
	// Go memory crosses for the duration of the call only, holding no Go
	// pointers; the ABI forbids the guest to retain or free it.
	buffer := responses.Get().(*[]byte)
	defer responses.Put(buffer)
	response := *buffer
	var request *byte
	if len(input) > 0 {
		request = &input[0]
	}
	var size uint32
	status := s.exports.call(s.handle, op, request, uint32(len(input)), &response[0], uint32(len(response)), &size)
	if status != abi.OK {
		return nil, fmt.Errorf("%w: C ABI status %d", ErrInvalid, status)
	}
	if size == 0 || size > uint32(len(response)) {
		return nil, ErrInvalid
	}
	return append([]byte(nil), response[:size]...), nil
}

func (s *session) close() { s.exports.close(s.handle) }
