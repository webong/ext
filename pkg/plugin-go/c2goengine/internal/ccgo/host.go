//go:build (darwin && amd64) || (darwin && arm64) || (linux && amd64) || (linux && arm64)

package ccgo

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
)

// This file is the primitive layer over the translated host (lib/plugin/src/host.c): Go values in
// and out, status codes for results. Everything C keeps a pointer to lives in C memory; a Go value
// the engine calls back into is held in the handle table and named to C by its handle. The meaning
// of a call (contexts, errors, sessions) is session.go's.

// HostAvailable is true where this package holds the translated host.
const HostAvailable = true

// Handle names a Go value to the engine: C holds the number and gives it back.
type Handle uintptr

var (
	handles       sync.Map // uintptr -> any
	handleCounter atomic.Uintptr
)

// NewHandle returns a handle for v, valid until Delete.
func NewHandle(v any) Handle {
	id := handleCounter.Add(1)
	handles.Store(id, v)
	return Handle(id)
}

// Value returns what the handle names, or nil after Delete.
func (h Handle) Value() any { v, _ := handles.Load(uintptr(h)); return v }

// Delete forgets the handle.
func (h Handle) Delete() { handles.Delete(uintptr(h)) }

// Statuses (lib/plugin/include/ext_host.h) that callers of this package name.
const (
	StatusOK       = stOK
	StatusInvalid  = stInvalid
	StatusDenied   = stDenied
	StatusClosed   = stClosed
	StatusTimeout  = stTimeout
	StatusIO       = stIO
	StatusNoMem    = stNoMem
	StatusCanceled = stCanceled
	StatusProtocol = stProtocol
	StatusMismatch = stMismatch
)

// StatusError names a status as the cgo binding does.
func StatusError(s int32) error { return statusError(s) }

// Policy decides on a JSON document; true allows.
type Policy func(data []byte) bool

// Hooks are the contextual policies and the observer of a host. The Handle is the one the call was
// started with (zero outside a call).
type Hooks struct {
	Verify    func(call Handle, data []byte) bool
	Authorize func(call Handle, data []byte) bool
	Observe   func(call Handle, data []byte)
}

// Emit hands the engine one document: a descriptor from Connect, a response from Invoke.
type Emit func(data []byte) int32

// Backend is the extension the engine drives: it connects and invokes, and the engine closes it
// (concurrently with a call, to interrupt it) and releases it.
type Backend interface {
	Connect(call Handle, timeoutMs uint32, emit Emit) int32
	Invoke(call Handle, request []byte, timeoutMs uint32, emit Emit) int32
	Close()
	Release()
}

// Extension backend flags (ext_host.h).
const (
	BackendConcurrent = 1
	BackendFrames     = 4
)

const (
	backendExtensionContext = 3
	backendProcessLaunch    = 5
)

// Host is a translated ext_host.
type Host struct {
	mu    sync.Mutex
	ptr   uintptr
	state Handle // *hostState: the Go side the engine calls back into
}

type hostState struct {
	verify, authorize Policy
	hooks             *Hooks
	backend           Backend
}

// The callbacks the engine calls are Go functions; C gets the address of their function value.
var (
	fVerify           = hostVerify
	fAuthorize        = hostAuthorize
	fVerifyContext    = hostVerifyContext
	fAuthorizeContext = hostAuthorizeContext
	fObserve          = hostObserve
	fConnect          = hostConnect
	fInvoke           = hostInvoke
	fCloseBackend     = hostCloseBackend
	fReleaseBackend   = hostReleaseBackend
)

func pointer[F any](f *F) uintptr { return *(*uintptr)(unsafe.Pointer(f)) }

func alloc(tls *libc.TLS, n uintptr) uintptr {
	if n == 0 {
		n = 1
	}
	return libc.Xcalloc(tls, 1, libc.Tsize_t(n))
}

func bytesAt(p uintptr, n size_t) []byte {
	if p == 0 || n == 0 {
		return nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))...)
}

func recovered(fallback int32, out *int32) {
	if recover() != nil {
		*out = fallback
	}
}

func stateOf(user uintptr) *hostState { s, _ := Handle(user).Value().(*hostState); return s }

func hostVerify(_ *libc.TLS, user, data uintptr, n size_t) (r int32) {
	r = 1
	defer recovered(1, &r)
	if s := stateOf(user); s != nil && s.verify != nil && s.verify(bytesAt(data, n)) {
		return 0
	}
	return 1
}

func hostAuthorize(_ *libc.TLS, user, data uintptr, n size_t) (r int32) {
	r = 1
	defer recovered(1, &r)
	if s := stateOf(user); s != nil && s.authorize != nil && s.authorize(bytesAt(data, n)) {
		return 0
	}
	return 1
}

func callOf(options uintptr) Handle {
	if options == 0 {
		return 0
	}
	return Handle((*ext_call_options)(unsafe.Pointer(options)).Fvalue)
}

func hostVerifyContext(_ *libc.TLS, user, options, data uintptr, n size_t) (r int32) {
	r = 1
	defer recovered(1, &r)
	if s := stateOf(user); s != nil && s.hooks != nil && s.hooks.Verify != nil && s.hooks.Verify(callOf(options), bytesAt(data, n)) {
		return 0
	}
	return 1
}

func hostAuthorizeContext(_ *libc.TLS, user, options, data uintptr, n size_t) (r int32) {
	r = 1
	defer recovered(1, &r)
	if s := stateOf(user); s != nil && s.hooks != nil && s.hooks.Authorize != nil && s.hooks.Authorize(callOf(options), bytesAt(data, n)) {
		return 0
	}
	return 1
}

func hostObserve(_ *libc.TLS, user, options, data uintptr, n size_t) {
	defer func() { _ = recover() }()
	if s := stateOf(user); s != nil && s.hooks != nil && s.hooks.Observe != nil {
		s.hooks.Observe(callOf(options), bytesAt(data, n))
	}
}

// emitter returns the Emit that hands a document to the engine's emit function pointer.
func emitter(tls *libc.TLS, emit, sink uintptr) Emit {
	return func(data []byte) int32 {
		if len(data) == 0 {
			return stInvalid
		}
		call := *(*func(*libc.TLS, uintptr, uintptr, size_t) int32)(unsafe.Pointer(&struct{ uintptr }{emit}))
		p, free := copyIn(tls, data)
		defer free()
		if p == 0 {
			return stNoMem
		}
		return call(tls, sink, p, size_t(len(data)))
	}
}

func hostConnect(tls *libc.TLS, user, options, emit, sink uintptr) (r int32) {
	r = stIO
	defer recovered(stIO, &r)
	s := stateOf(user)
	if s == nil || s.backend == nil {
		return stClosed
	}
	o := (*ext_call_options)(unsafe.Pointer(options))
	return s.backend.Connect(Handle(o.Fvalue), o.Ftimeout_ms, emitter(tls, emit, sink))
}

func hostInvoke(tls *libc.TLS, user, data uintptr, n size_t, options, emit, sink uintptr) (r int32) {
	r = stIO
	defer recovered(stIO, &r)
	s := stateOf(user)
	if s == nil || s.backend == nil {
		return stClosed
	}
	o := (*ext_call_options)(unsafe.Pointer(options))
	return s.backend.Invoke(Handle(o.Fvalue), bytesAt(data, n), o.Ftimeout_ms, emitter(tls, emit, sink))
}

func hostCloseBackend(_ *libc.TLS, user uintptr) {
	defer func() { _ = recover() }()
	if s := stateOf(user); s != nil && s.backend != nil {
		s.backend.Close()
	}
}

func hostReleaseBackend(_ *libc.TLS, user uintptr) {
	defer func() { _ = recover() }()
	if s := stateOf(user); s != nil && s.backend != nil {
		s.backend.Release()
	}
}

// Launch is what OpenProcess starts, in the engine's terms.
type Launch = LaunchOptions

func cStringArray(tls *libc.TLS, values []string) (uintptr, func(), error) {
	if len(values) == 0 {
		return 0, func() {}, nil
	}
	array := alloc(tls, uintptr(len(values))*unsafe.Sizeof(uintptr(0)))
	if array == 0 {
		return 0, nil, errors.New("c2goengine: out of memory")
	}
	var strs []uintptr
	free := func() {
		for _, p := range strs {
			libc.Xfree(tls, p)
		}
		libc.Xfree(tls, array)
	}
	for i, v := range values {
		if strings.IndexByte(v, 0) >= 0 {
			free()
			return 0, nil, errors.New("c2goengine: NUL in string")
		}
		p, err := libc.CString(v)
		if err != nil {
			free()
			return 0, nil, err
		}
		strs = append(strs, p)
		*(*uintptr)(unsafe.Pointer(array + uintptr(i)*unsafe.Sizeof(uintptr(0)))) = p
	}
	return array, free, nil
}

func create(descriptor []byte, st *hostState, kind uint32, config func(tls *libc.TLS, user uintptr) (uintptr, uintptr, func(), int32)) (*Host, int32) {
	if len(descriptor) == 0 {
		return nil, stInvalid
	}
	tls := acquire()
	defer release(tls)
	h := &Host{state: NewHandle(st)}
	cfg, cfgSize, free, status := config(tls, uintptr(h.state))
	if status != stOK {
		h.state.Delete()
		return nil, status
	}
	defer free()
	descriptorAddr, freeDescriptor := copyIn(tls, descriptor)
	defer freeDescriptor()
	options := alloc(tls, unsafe.Sizeof(ext_host_options{}))
	backend := alloc(tls, unsafe.Sizeof(ext_backend_options{}))
	out := alloc(tls, unsafe.Sizeof(uintptr(0)))
	defer libc.Xfree(tls, options)
	defer libc.Xfree(tls, backend)
	defer libc.Xfree(tls, out)
	if options == 0 || backend == 0 || out == 0 {
		h.state.Delete()
		return nil, stNoMem
	}
	*(*ext_host_options)(unsafe.Pointer(options)) = ext_host_options{
		Fabi_version:    2,
		Fstruct_size:    uint32_t(unsafe.Sizeof(ext_host_options{})),
		Fdescriptor:     descriptorAddr,
		Fdescriptor_len: size_t(len(descriptor)),
		Fverify:         pointer(&fVerify),
		Fauthorize:      pointer(&fAuthorize),
		Fuser:           uintptr(h.state),
	}
	*(*ext_backend_options)(unsafe.Pointer(backend)) = ext_backend_options{
		Fstruct_size: uint32_t(unsafe.Sizeof(ext_backend_options{})),
		Fkind:        kind,
		Fconfig:      cfg,
		Fconfig_size: size_t(cfgSize),
	}
	status = ext_host_create(tls, options, backend, out)
	if status != stOK {
		h.state.Delete()
		return nil, status
	}
	h.ptr = *(*uintptr)(unsafe.Pointer(out))
	return h, stOK
}

// NewBackendHost creates a host around an extension backend. The engine takes ownership of the
// backend: it closes and releases it. verify and authorize are the legacy policies the engine
// requires; SetHooks replaces them with contextual ones.
func NewBackendHost(descriptor []byte, flags uint32, b Backend, verify, authorize Policy) (*Host, int32) {
	st := &hostState{verify: verify, authorize: authorize, backend: b}
	return create(descriptor, st, backendExtensionContext, func(tls *libc.TLS, user uintptr) (uintptr, uintptr, func(), int32) {
		ext := alloc(tls, unsafe.Sizeof(ext_backend_extension_context{}))
		if ext == 0 {
			return 0, 0, nil, stNoMem
		}
		// user is the same handle as the host's: the backend and the policies share one state.
		*(*ext_backend_extension_context)(unsafe.Pointer(ext)) = ext_backend_extension_context{
			Fstruct_size: uint32_t(unsafe.Sizeof(ext_backend_extension_context{})),
			Fflags:       flags,
			Fuser:        user,
			Fconnect:     pointer(&fConnect),
			Finvoke:      pointer(&fInvoke),
			Fclose1:      pointer(&fCloseBackend),
			Frelease:     pointer(&fReleaseBackend),
		}
		return ext, unsafe.Sizeof(ext_backend_extension_context{}), func() { libc.Xfree(tls, ext) }, stOK
	})
}

// NewLaunchHost creates a host whose child process the engine owns.
func NewLaunchHost(l Launch, descriptor []byte, verify, authorize Policy) (*Host, int32) {
	if strings.IndexByte(l.Executable, 0) >= 0 || strings.IndexByte(l.Directory, 0) >= 0 ||
		len(l.Arguments) > 256 || len(l.Environment) > 256 || l.StderrBytes < 0 || l.StderrBytes > 16<<20 {
		return nil, stInvalid
	}
	st := &hostState{verify: verify, authorize: authorize}
	return create(descriptor, st, backendProcessLaunch, func(tls *libc.TLS, _ uintptr) (uintptr, uintptr, func(), int32) {
		path, err := libc.CString(l.Executable)
		if err != nil {
			return 0, 0, nil, stNoMem
		}
		var dir uintptr
		if l.Directory != "" {
			if dir, err = libc.CString(l.Directory); err != nil {
				libc.Xfree(tls, path)
				return 0, 0, nil, stNoMem
			}
		}
		args, freeArgs, err := cStringArray(tls, l.Arguments)
		if err != nil {
			return 0, 0, nil, stInvalid
		}
		env, freeEnv, err := cStringArray(tls, l.Environment)
		if err != nil {
			freeArgs()
			return 0, 0, nil, stInvalid
		}
		cfg := alloc(tls, unsafe.Sizeof(ext_jsonline_process_launch{}))
		*(*ext_jsonline_process_launch)(unsafe.Pointer(cfg)) = ext_jsonline_process_launch{
			Fstruct_size:       uint32_t(unsafe.Sizeof(ext_jsonline_process_launch{})),
			Fexecutable:        path,
			Farguments:         args,
			Fargument_count:    size_t(len(l.Arguments)),
			Fenvironment:       env,
			Fenvironment_count: size_t(len(l.Environment)),
			Fworking_directory: dir,
			Fstderr_bytes:      uint32_t(l.StderrBytes),
		}
		free := func() {
			libc.Xfree(tls, cfg)
			freeEnv()
			freeArgs()
			if dir != 0 {
				libc.Xfree(tls, dir)
			}
			libc.Xfree(tls, path)
		}
		return cfg, unsafe.Sizeof(ext_jsonline_process_launch{}), free, stOK
	})
}

// SetHooks configures the contextual policies and the observer, once, before the host starts.
func (h *Host) SetHooks(hooks Hooks) int32 {
	st := stateOf(uintptr(h.state))
	st.hooks = &hooks
	tls := acquire()
	defer release(tls)
	p := alloc(tls, unsafe.Sizeof(ext_host_hooks{}))
	if p == 0 {
		return stNoMem
	}
	defer libc.Xfree(tls, p)
	*(*ext_host_hooks)(unsafe.Pointer(p)) = ext_host_hooks{
		Fstruct_size: uint32_t(unsafe.Sizeof(ext_host_hooks{})),
		Fuser:        uintptr(h.state),
		Fverify:      pointer(&fVerifyContext),
		Fauthorize:   pointer(&fAuthorizeContext),
		Fobserve:     pointer(&fObserve),
	}
	return ext_host_set_hooks(tls, h.ptr, p)
}

// Options is the native call options and cancel signal of one operation.
type Options struct {
	options uintptr
	cancel  uintptr
}

// NewOptions makes the options of a call that may take timeoutMs and carries value to the callbacks.
func NewOptions(timeoutMs uint32, value Handle) (*Options, int32) {
	tls := acquire()
	defer release(tls)
	c := &Options{}
	out := alloc(tls, unsafe.Sizeof(uintptr(0)))
	if out == 0 {
		return nil, stNoMem
	}
	defer libc.Xfree(tls, out)
	if s := ext_cancel_create(tls, out); s != stOK {
		return nil, s
	}
	c.cancel = *(*uintptr)(unsafe.Pointer(out))
	c.options = alloc(tls, unsafe.Sizeof(ext_call_options{}))
	*(*ext_call_options)(unsafe.Pointer(c.options)) = ext_call_options{
		Fstruct_size: uint32_t(unsafe.Sizeof(ext_call_options{})),
		Ftimeout_ms:  timeoutMs,
		Fcancel:      c.cancel,
		Fvalue:       uintptr(value),
	}
	return c, stOK
}

// Signal tells the engine the call is canceled. Safe from any goroutine until Free.
func (c *Options) Signal() {
	tls := acquire()
	defer release(tls)
	ext_cancel_signal(tls, c.cancel)
}

// Free releases the call's native memory, after everything that uses it has joined.
func (c *Options) Free() {
	tls := acquire()
	defer release(tls)
	ext_cancel_destroy(tls, c.cancel)
	libc.Xfree(tls, c.options)
}

// Start handshakes with the backend.
func (h *Host) Start(c *Options) int32 {
	tls := acquire()
	defer release(tls)
	return ext_host_start_with_options(tls, h.ptr, c.options)
}

// Drain stops admission and waits for admitted calls, bounded by the call's options.
func (h *Host) Drain(c *Options) int32 {
	tls := acquire()
	defer release(tls)
	return ext_host_drain_with_options(tls, h.ptr, c.options)
}

// DrainFor is Drain bounded by a duration in milliseconds, without a context.
func (h *Host) DrainFor(ms uint32) int32 {
	tls := acquire()
	defer release(tls)
	return ext_host_drain(tls, h.ptr, ms)
}

func (h *Host) takeBuffer(tls *libc.TLS, out uintptr) []byte {
	buffer := (*ext_buffer)(unsafe.Pointer(out))
	data := bytesAt(buffer.Fdata, buffer.Flen1)
	ext_buffer_free(tls, out)
	return data
}

// Invoke sends a complete request envelope and returns the response envelope.
func (h *Host) Invoke(request []byte, c *Options) ([]byte, int32) {
	if len(request) == 0 {
		return nil, stInvalid
	}
	tls := acquire()
	defer release(tls)
	out := alloc(tls, unsafe.Sizeof(ext_buffer{}))
	defer libc.Xfree(tls, out)
	in, freeIn := copyIn(tls, request)
	defer freeIn()
	status := ext_host_invoke_with_options(tls, h.ptr, in, size_t(len(request)), c.options, out)
	return h.takeBuffer(tls, out), status
}

// Call sends {contract, operation, payload}; the engine supplies identity, surface, deadline and id,
// and returns a complete response envelope. Progress events are counted and dropped.
func (h *Host) Call(request []byte, c *Options) ([]byte, int32) {
	if len(request) == 0 {
		return nil, stInvalid
	}
	tls := acquire()
	defer release(tls)
	out := alloc(tls, unsafe.Sizeof(ext_buffer{}))
	progress := alloc(tls, unsafe.Sizeof(ext_progress_options{}))
	defer libc.Xfree(tls, out)
	defer libc.Xfree(tls, progress)
	*(*ext_progress_options)(unsafe.Pointer(progress)) = ext_progress_options{Fstruct_size: uint32_t(unsafe.Sizeof(ext_progress_options{}))}
	in, freeIn := copyIn(tls, request)
	defer freeIn()
	status := ext_host_call_progress(tls, h.ptr, in, size_t(len(request)), c.options, progress, out)
	return h.takeBuffer(tls, out), status
}

// State is the engine's lifecycle state, one of the ext_host_state values.
func (h *Host) State() int32 {
	tls := acquire()
	defer release(tls)
	return ext_host_get_state(tls, h.ptr)
}

// Host states (ext_host.h).
const (
	StateCreated  = 0
	StateReady    = 1
	StateDraining = 2
	StateClosed   = 3
	StateFailed   = 4
)

// Close cancels the host: it interrupts pending work and closes the backend.
func (h *Host) Close() {
	tls := acquire()
	defer release(tls)
	ext_host_close(tls, h.ptr)
}

// Destroy waits for active calls and releases the host; the Host is unusable after.
func (h *Host) Destroy() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ptr == 0 {
		return
	}
	tls := acquire()
	defer release(tls)
	ext_host_destroy(tls, h.ptr)
	h.ptr = 0
	h.state.Delete()
}

// Stderr returns the retained stderr of a launched child.
func (h *Host) Stderr() string {
	tls := acquire()
	defer release(tls)
	n := alloc(tls, unsafe.Sizeof(size_t(0)))
	defer libc.Xfree(tls, n)
	if ext_host_process_stderr(tls, h.ptr, 0, 0, n) != stOK {
		return ""
	}
	size := *(*size_t)(unsafe.Pointer(n))
	if size == 0 {
		return ""
	}
	buffer := alloc(tls, uintptr(size))
	defer libc.Xfree(tls, buffer)
	if ext_host_process_stderr(tls, h.ptr, buffer, size, n) != stOK {
		return ""
	}
	return string(bytesAt(buffer, min(*(*size_t)(unsafe.Pointer(n)), size)))
}
