package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/guest/instance"
)

// LibraryName is lib/plugin's C prefix: the name its copy is provided under (library.Provide, which
// the package ext-go fetch writes for purego calls) and the prefix of its library interface.
const LibraryName = "ext_plugin"

// LibraryABIVersion is the version of lib/plugin's library interface this package speaks
// (EXT_PLUGIN_ABI_VERSION).
const LibraryABIVersion = 1

var pureGoLibrary struct {
	mu   sync.Mutex
	path string
	once *sync.Once
	ifc  *library.Interface
	err  error
}

// UseLibrary names the built lib/plugin shared library (libext_plugin_host.so, .dylib or
// ext_plugin_host.dll) the purego engine loads. Without it, the purego engine loads the copy the
// program carries (library.Provide under LibraryName). Call it before the purego engine is first
// used; the library a program has loaded is never unloaded or replaced.
func UseLibrary(path string) {
	pureGoLibrary.mu.Lock()
	defer pureGoLibrary.mu.Unlock()
	if pureGoLibrary.once == nil {
		pureGoLibrary.path = path
	}
}

// pureGoInterface loads and binds the library once.
func pureGoInterface() (*library.Interface, error) {
	pureGoLibrary.mu.Lock()
	if pureGoLibrary.once == nil {
		pureGoLibrary.once = new(sync.Once)
	}
	once, path := pureGoLibrary.once, pureGoLibrary.path
	pureGoLibrary.mu.Unlock()
	once.Do(func() {
		var lib *library.Library
		var err error
		if path != "" {
			lib, err = library.Open(path)
		} else {
			lib, err = library.OpenProvided(LibraryName, "")
		}
		if err == nil {
			pureGoLibrary.ifc, err = library.BindInterface(lib, LibraryName, LibraryABIVersion)
		}
		pureGoLibrary.err = err
	})
	return pureGoLibrary.ifc, pureGoLibrary.err
}

// pureGoEngine is the C engine loaded at run time through purego: no cgo, no C compiler, and the
// engine itself rather than a translation of it. It runs sessions over a child process the engine
// starts (OpenProcess); a Go backend (Open, NewInstances) would need C to call Go, which it never does.
type pureGoEngine struct{}

func (pureGoEngine) sealed()    {}
func (pureGoEngine) Name() Name { return PureGo }

// Available reports whether this build can load a C library and a library is named (UseLibrary) or
// carried (library.Provide). It does not load it: a library that fails to load is the error of the
// first call.
func (pureGoEngine) Available() bool {
	if !library.Supported() {
		return false
	}
	pureGoLibrary.mu.Lock()
	path := pureGoLibrary.path
	pureGoLibrary.mu.Unlock()
	if path != "" {
		return true
	}
	_, ok := library.Provided(LibraryName)
	return ok
}

func (e pureGoEngine) Features() Feature {
	if !e.Available() {
		return 0
	}
	return Services | Sessions | Progress | Reverse
}

func (e pureGoEngine) Call(operation string, input json.RawMessage) (json.RawMessage, error) {
	if !e.Available() {
		return nil, errUnsupported(e, "services")
	}
	ifc, err := pureGoInterface()
	if err != nil {
		return nil, fmt.Errorf("plugin: purego: %w", err)
	}
	return interfaceServices(ifc, operation, input)
}

func (e pureGoEngine) Open(context.Context, engine.Descriptor, engine.Options) (Session, error) {
	return nil, errUnsupported(e, "sessions over a Go backend (use OpenProcess)")
}

func (e pureGoEngine) OpenProcess(ctx context.Context, d engine.Descriptor, launch Launch, options engine.Options) (Session, error) {
	if !e.Available() {
		return nil, errUnsupported(e, "sessions")
	}
	ifc, err := pureGoInterface()
	if err != nil {
		return nil, fmt.Errorf("plugin: purego: %w", err)
	}
	return openInterfaceSession(ctx, PureGo, ifc, d, launch, options)
}

func newPureGoInstances[T any](e pureGoEngine, _ instance.Options[T]) (Instances[T], error) {
	return nil, errUnsupported(e, "instances over a Go backend")
}
