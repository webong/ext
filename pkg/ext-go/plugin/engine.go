// Package plugin chooses which implementation of the plugin engine runs a program's sessions.
//
// The engine is reached four ways, and each is its own module (or, for purego, a library the program
// names or carries) so an application takes only the ones it needs:
//
//	cgo     the C engine through cgo (pkg/ext-go/cgoengine): the C itself, at native speed
//	c2go    the C engine translated to Go by ccgo (pkg/ext-go/c2goengine): no cgo and no C compiler
//	purego  the built C engine loaded at run time through its library interface (UseLibrary, or the
//	        copy ext-go fetch embeds): no cgo and no C compiler; sessions over a child process only
//	go      the pure-Go engine (pkg/ext-go/engine): the oracle the others are held against, and
//	        the one that exists on every platform
//
// This package is the one place that knows all four: it lists them as the plugin engine's drivers and
// chooses with core/driver, the generic switcher every library of lib/ shares. Engines lists them in
// the order Choose prefers them; Choose picks one by name or, without a name, the best available one that has the
// features the caller needs; FromEnvironment reads the name from EXT_ENGINE. The choice is made once,
// when a session or an instance manager is opened, and it is visible: Session.Engine names the engine
// that runs the session. An engine is never replaced by another halfway through a session, and Choose
// never falls back silently: a name that cannot be served is an error.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/webong/ext/pkg/ext-go/core/driver"
	engine "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/guest/instance"
)

// Name identifies an engine: one of core/driver's drivers.
type Name = driver.Name

const (
	// CGo is the C engine linked through cgo (the ext_cengine build tag and a C library).
	CGo = driver.CGo
	// C2Go is the C engine translated to Go.
	C2Go = driver.C2Go
	// PureGo is the built C engine loaded through purego.
	PureGo = driver.PureGo
	// Go is the pure-Go engine, the oracle.
	Go = driver.Go
)

// Auto asks Choose for the best available engine.
const Auto = driver.Auto

// Environment is the variable FromEnvironment reads: an engine name, or auto (the default).
const Environment = driver.Environment

// Feature is something an engine may or may not carry. An engine that is not available has none.
type Feature uint

const (
	// Services is Call: the engine's pure services (descriptor, request, response, frame, manifest,
	// schema and endpoint rules) run by name on bounded JSON. The pure-Go engine has none: its rules
	// are the Go functions of the contract.
	Services Feature = 1 << iota
	// Sessions is OpenProcess: a session over a child process the engine starts.
	Sessions
	// Progress is Session.CallProgress and plugins that declare progress events.
	Progress
	// Reverse is engine.Options.Reverse and plugins that declare reverse operations.
	Reverse
	// Backends is Open, a session over a Go backend (options.Connect), and NewInstances. An engine
	// that runs only C (purego) has sessions without it: a Go backend needs C to call Go.
	Backends
)

// Has reports whether f holds every feature of want.
func (f Feature) Has(want Feature) bool { return f&want == want }

func (f Feature) String() string {
	var names []string
	for _, n := range []struct {
		f    Feature
		name string
	}{{Services, "services"}, {Sessions, "sessions"}, {Progress, "progress"}, {Reverse, "reverse"}, {Backends, "backends"}} {
		if f&n.f != 0 {
			names = append(names, n.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, "+")
}

// Launch starts a reviewed executable as the plugin's child process, the way an installed package is
// run. Executable must be the absolute path of a regular file. The host environment is never
// inherited: Environment entries are added to a scrubbed base of PATH.
type Launch struct {
	Executable  string
	Arguments   []string
	Environment []string
	// Directory is the child's working directory; empty inherits the host's. It must be absolute.
	Directory string
	// StderrBytes bounds the retained stderr; zero retains 4096 bytes.
	StderrBytes int
}

// Engine is one implementation of the plugin engine. Only this package implements it.
type Engine interface {
	// Name says which engine this is.
	Name() Name
	// Available reports whether this build holds the engine: the cgo engine needs cgo, the
	// ext_cengine tag and the C library; the translated engine needs a platform it was translated for.
	Available() bool
	// Features is what the engine carries here; none when it is not available.
	Features() Feature

	// Call runs one of the engine's pure services on bounded JSON. No plugin is executed. It needs
	// Services; see lib/plugin/services.md for the services.
	Call(operation string, input json.RawMessage) (json.RawMessage, error)
	// Open admits a plugin over the backend options.Connect supplies. It needs Sessions and Backends.
	Open(ctx context.Context, d engine.Descriptor, options engine.Options) (Session, error)
	// OpenProcess is Open with a child process the engine itself starts; options.Connect is unused.
	// It needs Sessions.
	OpenProcess(ctx context.Context, d engine.Descriptor, launch Launch, options engine.Options) (Session, error)

	// sealed keeps the set closed: NewInstances is generic, which methods cannot be, so it switches on
	// the engines this package defines.
	sealed()
}

// Session is one admitted connection to a plugin, whichever engine runs it.
type Session interface {
	// Engine names the engine that runs this session.
	Engine() Name
	Descriptor() engine.Descriptor
	State() engine.State
	// Done is closed once the session has been closed or aborted.
	Done() <-chan struct{}
	Call(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error)
	// CallProgress is Call for an operation that declares progress. It needs Progress; an engine
	// without it reports engine.ErrUnsupported.
	CallProgress(ctx context.Context, contract engine.ContractRef, operation string, payload json.RawMessage, onProgress func(engine.Progress), options ...engine.CallOptions) (json.RawMessage, error)
	// Close drains the session and frees it; Abort stops work and then frees it.
	Close(ctx context.Context) error
	Abort() error
	// Stderr is the retained, bounded and possibly truncated stderr of a session opened with
	// OpenProcess; empty otherwise. Treat it as untrusted text.
	Stderr() string
}

// Instances binds typed Go resource factories to leases, replacement, capacity and cleanup, as
// instance.Manager does, on the engine that created it. It satisfies capability.Configurer.
type Instances[T any] interface {
	Configure(ctx context.Context, key, revision string, config json.RawMessage) error
	Acquire(key string) (*Lease[T], error)
	Remove(key string) error
	Close(ctx context.Context) error
}

// Lease holds an instance until Release; Release reports the error of disposing of a retired one.
type Lease[T any] struct {
	Value    T
	Revision string
	release  func() error
}

// Release returns the lease. Releasing it twice is the underlying manager's error to report.
func (l *Lease[T]) Release() error { return l.release() }

// NewInstances creates a manager of at most options.Capacity instances on e. It needs Sessions and
// Backends.
func NewInstances[T any](e Engine, options instance.Options[T]) (Instances[T], error) {
	switch e := e.(type) {
	case goEngine:
		return newGoInstances(e, options)
	case cgoEngine:
		return newCGoInstances(e, options)
	case c2goEngine:
		return newC2GoInstances(e, options)
	case pureGoEngine:
		return newPureGoInstances(e, options)
	}
	return nil, fmt.Errorf("plugin: %v: %w", e, engine.ErrUnsupported)
}

// Engines lists every engine, available or not, in the order Choose prefers them: cgo, c2go, purego,
// go.
func Engines() []Engine { return []Engine{cgoEngine{}, c2goEngine{}, pureGoEngine{}, goEngine{}} }

// Get returns the engine called name, which must be one of CGo, C2Go, PureGo and Go. It does not check that
// the engine is available.
func Get(name Name) (Engine, error) {
	for _, e := range Engines() {
		if e.Name() == name {
			return e, nil
		}
	}
	return nil, fmt.Errorf("plugin: unknown engine %q (want %s, %s, %s, %s or %s): %w", name, CGo, C2Go, PureGo, Go, Auto, engine.ErrInvalid)
}

// Choose returns the engine to use. With Auto (or an empty name) it is the best available engine,
// in the order cgo, c2go, purego, go, that carries every feature in need. With a name it is that engine,
// and an engine that is not available, or lacks a feature, is an error that wraps
// engine.ErrUnsupported: asking for an engine by name never gets another one. A name that is no
// engine wraps engine.ErrInvalid.
func Choose(name Name, need Feature) (Engine, error) {
	if name != "" && name != Auto {
		if _, err := Get(name); err != nil {
			return nil, err
		}
	}
	e, err := driver.Choose(Engines(), name, func(e Engine) error {
		if !e.Features().Has(need) {
			return fmt.Errorf("lacks %s (it has %s)", need&^e.Features(), e.Features())
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, driver.ErrUnknown) {
			return nil, fmt.Errorf("plugin: %w: %w", err, engine.ErrInvalid)
		}
		return nil, fmt.Errorf("plugin: %w (need %s): %w", err, need, engine.ErrUnsupported)
	}
	return e, nil
}

// FromEnvironment is Choose with the name taken from EXT_ENGINE: cgo, c2go, purego, go or auto. Unset or empty
// is auto.
func FromEnvironment(need Feature) (Engine, error) {
	name, err := driver.FromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("plugin: %w: %w", err, engine.ErrInvalid)
	}
	return Choose(name, need)
}

// errUnsupported is what an engine that lacks a feature reports.
func errUnsupported(e Engine, what string) error {
	return fmt.Errorf("plugin: %s does not carry %s here: %w", e.Name(), what, engine.ErrUnsupported)
}

// errUnsupportedBy is errUnsupported for a session, which knows its engine's name and not the engine.
func errUnsupportedBy(name Name, what string) error {
	return fmt.Errorf("plugin: %s does not carry %s here: %w", name, what, engine.ErrUnsupported)
}
