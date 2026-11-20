// Package yaegi runs a guest shipped as Go source, interpreted in-process by
// yaegi (github.com/traefik/yaegi). The guest is ordinary Go written against
// the engine's guest API, the same source a compiled guest uses, and it is
// served through engine/transport/inprocess as engine/host/nativego serves a
// loaded image.
//
// The guest's package sits at the root of a directory, normally the tree
// artifact named by an entrypoint whose runtime is Runtime ("go-source"). It
// exports the factory nativego requires:
//
//	func CTXPlugin(context.Context) (plugin.Backend, error)
//
// Its imports are checked before anything is evaluated: only the standard
// library packages in Options.Packages (DefaultPackages when nil), the ext
// guest packages (GuestPackages) and the packages the host adds in
// Options.Symbols are importable. os/exec, unsafe, syscall, reflect, plugin,
// os and the other packages that reach outside the interpreter are not in
// DefaultPackages, so a guest gets them only when the host lists them. Package
// variables are copied into each interpreter, so a guest that assigns one
// changes its own copy and never the host's.
//
// This is a limit on what interpreted code can name, not a sandbox: a guest
// shares the host's memory and scheduler, can allocate without bound and can
// block inside compiled code. Run only reviewed guests.
//
// Cancellation stops the interpreter. A call whose context ends before it
// returns, a panic in the guest and Close each stop every interpreted
// goroutine of the backend at its next step, through EvalWithContext, and the
// backend fails every later call, as a session fails on a canceled call. An
// interpreted goroutine blocked inside compiled code (a long time.Sleep, a
// read) cannot be forcibly stopped: it finishes when that call returns, and its
// result is discarded. The calling goroutine returns at once.
package yaegi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess"
	"github.com/webong/ext/pkg/ext-go/yaegi/internal/symbols"
)

// Runtime is the entrypoint runtime of a package whose entrypoint artifact is
// a tree of Go source for this backend.
const Runtime = "go-source"

// Symbol is the factory the guest package exports, as for nativego:
//
//	func CTXPlugin(context.Context) (plugin.Backend, error)
//
// Each call must create an independent backend. The factory context bounds
// startup, not the guest's lifetime.
const Symbol = "CTXPlugin"

// ErrStopped is returned by every call after the interpreter has been stopped.
var ErrStopped = errors.New("yaegi: interpreter stopped")

// Options configures the interpreter. The zero value uses DefaultPackages, no
// call timeout and discards the guest's standard output and error.
type Options struct {
	// Packages lists the standard library import paths the guest may import,
	// from those yaegi exports. Nil means DefaultPackages; an empty, non-nil
	// list allows none. Listing a package such as "os" or "os/exec" is the
	// host's explicit grant.
	Packages []string
	// Symbols adds packages the host exports to the guest, keyed as yaegi
	// keys them ("<import path>/<package name>"). Each one becomes importable.
	// Host symbols replace a standard library package of the same path.
	Symbols interp.Exports
	// Timeout bounds each call that has no earlier deadline. Zero leaves calls
	// bounded only by the caller's context.
	Timeout time.Duration
	// Stdout and Stderr receive what the guest prints through fmt and log.
	// Nil discards it.
	Stdout, Stderr io.Writer
	// MaxSourceBytes bounds the total size of the source directory and
	// MaxSourceFiles its number of files. Zero uses 16 MiB and 4096.
	MaxSourceBytes int64
	MaxSourceFiles int
}

// DefaultPackages is the standard library a guest may import when
// Options.Packages is nil: computation, encoding, text, time and
// synchronization. It holds nothing that reaches the filesystem, the network,
// processes, the environment, the runtime or reflection.
var DefaultPackages = []string{
	"bufio", "bytes", "cmp", "container/heap", "container/list", "container/ring",
	"context", "crypto/hmac", "crypto/md5", "crypto/sha1", "crypto/sha256", "crypto/sha512",
	"encoding/base32", "encoding/base64", "encoding/binary", "encoding/csv", "encoding/hex", "encoding/json",
	"errors", "fmt", "hash", "hash/crc32", "hash/fnv", "html", "io", "maps", "math", "math/big",
	"math/bits", "math/rand", "net/url", "path", "regexp", "slices", "sort", "strconv", "strings",
	"sync", "sync/atomic", "text/template", "time", "unicode", "unicode/utf16", "unicode/utf8",
}

// GuestPackages are the ext packages every guest may import: the engine's
// root package, its guest packages, the in-process binding a factory wraps its
// guest in (inprocess.New) and the schema types. Their generic
// functions (author.Register, instance.New and the like) are not exported to
// interpreted code; a guest builds its handler with plugin.NewGuest.
var GuestPackages = []string{
	"github.com/webong/ext/pkg/ext-go/engine",
	"github.com/webong/ext/pkg/ext-go/engine/guest/author",
	"github.com/webong/ext/pkg/ext-go/engine/guest/capability",
	"github.com/webong/ext/pkg/ext-go/engine/guest/instance",
	"github.com/webong/ext/pkg/ext-go/engine/guest/stream",
	"github.com/webong/ext/pkg/ext-go/engine/transport/inprocess",
	"github.com/webong/ext/pkg/ext-go/engine/validate/schema",
}

// genericSource are the standard library packages yaegi compiles from source
// rather than exporting; they are importable only when listed.
var genericSource = map[string]bool{"cmp": true, "maps": true, "slices": true}

// guestImport is the import path the guest's directory is evaluated under, and
// parkImport the host package the supervisor uses. Neither is importable by
// the guest: the import check refuses both.
const (
	guestImport = "ext.guest"
	parkImport  = "github.com/webong/ext/pkg/ext-go/yaegi/internal/park"
)

// Backend is an interpreted guest behind an in-process binding.
type Backend struct {
	inner   *inprocess.Backend
	guest   plugin.Backend
	timeout time.Duration
	stderr  io.Writer

	halt    context.CancelFunc // ends the supervisor's EvalWithContext
	parked  chan struct{}      // closed once the supervisor has returned
	stopped atomic.Bool
	once    sync.Once
	err     error
}

// Open reads the guest source below dir, an absolute directory the host has
// verified, checks it against digest (plugin.DirectoryDigest of the directory,
// as a tree artifact records it), and evaluates it. What is evaluated is what
// was hashed: the files are read once, into memory. Use it inside
// plugin.Options.Connect so verification precedes evaluation, or use
// OpenPackage. ctx bounds reading, evaluation and the factory.
func Open(ctx context.Context, dir, digest string, options Options) (*Backend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
		return nil, fmt.Errorf("%w: Go source requires an absolute directory", plugin.ErrInvalid)
	}
	source, err := readSource(dir, options)
	if err != nil {
		return nil, err
	}
	if source.digest != digest {
		return nil, fmt.Errorf("%w: Go source digest", plugin.ErrMismatch)
	}
	allowed, exports, err := exportsFor(options)
	if err != nil {
		return nil, err
	}
	if err := checkImports(source, allowed); err != nil {
		return nil, err
	}
	return start(ctx, source, exports, options)
}

func start(ctx context.Context, source *source, exports interp.Exports, options Options) (*Backend, error) {
	stdout, stderr := options.Stdout, options.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	i := interp.New(interp.Options{
		GoPath:               ".",
		SourcecodeFilesystem: source.fs(guestImport),
		Stdin:                strings.NewReader(""),
		Stdout:               stdout,
		Stderr:               stderr,
		Args:                 []string{},
		Env:                  []string{},
	})
	ready := make(chan struct{})
	var readyOnce sync.Once
	exports[parkImport+"/park"] = map[string]reflect.Value{
		"Ready": reflect.ValueOf(func() { readyOnce.Do(func() { close(ready) }) }),
	}
	if err := i.Use(exports); err != nil {
		return nil, fmt.Errorf("%w: %v", plugin.ErrInvalid, err)
	}
	if _, err := i.EvalWithContext(ctx, `import guest "`+guestImport+`"`); err != nil {
		return nil, loadError(ctx, err)
	}
	value, err := i.EvalWithContext(ctx, "guest."+Symbol)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", plugin.ErrUnsupported, Symbol, err)
	}
	factory, ok := value.Interface().(func(context.Context) (plugin.Backend, error))
	if !ok {
		return nil, fmt.Errorf("%w: %s must be func(context.Context) (plugin.Backend, error)", plugin.ErrUnsupported, Symbol)
	}
	if _, err := i.EvalWithContext(ctx, `import park "`+parkImport+`"`); err != nil {
		return nil, loadError(ctx, err)
	}
	// The supervisor is the interpreter's last evaluation, and the only way to
	// stop it: ending its context makes EvalWithContext stop every interpreted
	// goroutine. It runs for the backend's lifetime. The factory is called once
	// it runs, so every frame the guest creates watches the supervisor.
	life, halt := context.WithCancel(context.Background())
	b := &Backend{timeout: options.Timeout, stderr: stderr, halt: halt, parked: make(chan struct{})}
	go func() {
		defer close(b.parked)
		_, _ = i.EvalWithContext(life, "park.Ready(); select {}")
	}()
	select {
	case <-ready:
	case <-b.parked:
		return nil, fmt.Errorf("%w: interpreter supervisor did not start", plugin.ErrInvalid)
	case <-ctx.Done():
		b.stop()
		return nil, ctx.Err()
	}
	guest, err := b.run(ctx, func(ctx context.Context) (any, error) {
		g, err := factory(ctx)
		if err != nil && g != nil {
			_ = g.Close()
		}
		return g, err
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		b.stop()
		return nil, err
	}
	g, _ := guest.(plugin.Backend)
	if g == nil {
		b.stop()
		return nil, plugin.ErrInvalid
	}
	bound, err := inprocess.New(g)
	if err != nil {
		b.stop()
		return nil, err
	}
	b.guest, b.inner = g, bound
	return b, nil
}

func loadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: evaluate Go source: %v", plugin.ErrInvalid, err)
}

// run calls f, which enters interpreted code, on its own goroutine. When ctx
// ends first, or f panics, the interpreter is stopped and the call fails.
func (b *Backend) run(ctx context.Context, f func(context.Context) (any, error)) (any, error) {
	if b.stopped.Load() {
		return nil, ErrStopped
	}
	type result struct {
		value any
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(b.stderr, "yaegi: guest panic: %v\n%s", r, debug.Stack())
				done <- result{err: fmt.Errorf("%w: guest panicked", ErrStopped)}
			}
		}()
		v, err := f(ctx)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if errors.Is(r.err, ErrStopped) {
			b.stop()
		}
		if b.stopped.Load() && r.err == nil {
			return nil, ErrStopped
		}
		return r.value, r.err
	case <-ctx.Done():
		b.stop()
		return nil, ctx.Err()
	}
}

// bound applies Options.Timeout to a call without an earlier deadline.
func (b *Backend) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if b.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, b.timeout)
}

// stop ends the supervisor, which stops the interpreter, and waits for it.
func (b *Backend) stop() {
	b.stopped.Store(true)
	b.halt()
	<-b.parked
}

func (b *Backend) Handshake(ctx context.Context) (plugin.Descriptor, error) {
	ctx, cancel := b.bound(ctx)
	defer cancel()
	v, err := b.run(ctx, func(ctx context.Context) (any, error) { return b.inner.Handshake(ctx) })
	if err != nil {
		return plugin.Descriptor{}, err
	}
	return v.(plugin.Descriptor), nil
}

func (b *Backend) Invoke(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	ctx, cancel := b.bound(ctx)
	defer cancel()
	v, err := b.run(ctx, func(ctx context.Context) (any, error) { return b.inner.Invoke(ctx, r) })
	if err != nil {
		return plugin.Response{}, err
	}
	return v.(plugin.Response), nil
}

// Close cancels calls in flight, closes the guest and stops the interpreter.
// The guest's Close must return promptly; it is not called once the
// interpreter has stopped, since interpreted code no longer runs.
func (b *Backend) Close() error {
	b.once.Do(func() {
		_ = b.inner.Close()
		if !b.stopped.Load() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, b.err = b.run(ctx, func(context.Context) (any, error) { return nil, b.guest.Close() })
			cancel()
		}
		b.stop()
	})
	return b.err
}

// Profile describes the backend for packagekit.Environment.Runtimes, under
// Runtime.
func Profile() plugin.BackendProfile {
	return plugin.BackendProfile{Name: "yaegi", Protocols: []string{plugin.APIVersion}, Concurrent: true, Cancellation: "interpreter", ProcessOwner: "host", Boundary: plugin.BoundaryInProcess}
}

// exportsFor returns the importable paths and a fresh copy of their exports.
func exportsFor(options Options) (map[string]bool, interp.Exports, error) {
	packages := options.Packages
	if packages == nil {
		packages = DefaultPackages
	}
	byPath := func(table map[string]map[string]reflect.Value) map[string]string {
		keys := map[string]string{}
		for key := range table {
			if i := strings.LastIndexByte(key, '/'); i > 0 {
				keys[key[:i]] = key
			}
		}
		return keys
	}
	std := byPath(stdlib.Symbols)
	allowed := map[string]bool{}
	exports := interp.Exports{}
	for _, p := range packages {
		if genericSource[p] {
			allowed[p] = true
			continue
		}
		key, ok := std[p]
		if !ok {
			return nil, nil, fmt.Errorf("%w: yaegi exports no standard package %q", plugin.ErrUnsupported, p)
		}
		allowed[p] = true
		exports[key] = isolate(stdlib.Symbols[key])
	}
	// fmt is always loaded, unimportable unless listed: yaegi redirects its
	// output and compiles its generic packages only when fmt is present.
	if key := std["fmt"]; exports[key] == nil {
		exports[key] = isolate(stdlib.Symbols[key])
	}
	exports["."] = stdlib.Symbols["."]
	for key, table := range symbols.Symbols {
		allowed[key[:strings.LastIndexByte(key, '/')]] = true
		exports[key] = isolate(table)
	}
	for key, table := range options.Symbols {
		i := strings.LastIndexByte(key, '/')
		if i <= 0 || key == "." {
			return nil, nil, fmt.Errorf("%w: host symbols key %q must be <import path>/<package name>", plugin.ErrInvalid, key)
		}
		if old := std[key[:i]]; old != "" && old != key {
			delete(exports, old)
		}
		allowed[key[:i]] = true
		exports[key] = isolate(table)
	}
	return allowed, exports, nil
}

// isolate copies a package's exports, giving each package variable a copy of
// its own: a guest that assigns io.EOF or plugin.ErrDenied changes its copy.
func isolate(table map[string]reflect.Value) map[string]reflect.Value {
	out := make(map[string]reflect.Value, len(table))
	for name, value := range table {
		if value.IsValid() && value.CanAddr() {
			c := reflect.New(value.Type()).Elem()
			c.Set(value)
			value = c
		}
		out[name] = value
	}
	return out
}
