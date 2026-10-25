// Package engine chooses which implementation of the plugin engine runs a program's sessions.
//
// The engine's rules exist three times, and each is its own module so an application takes only the
// ones it needs:
//
//	go    the pure-Go contract (pkg/plugin-go/contract): the oracle the others are held against, and
//	      the one that exists on every platform
//	cgo   the C engine through cgo (pkg/plugin-go/cgoengine): the C itself, at native speed
//	c2go  the C engine translated to Go by ccgo (pkg/plugin-go/c2goengine): no cgo and no C compiler
//
// This package is the one place that knows all three. Engines lists them in the order Choose
// prefers them; Choose picks one by name or, without a name, the best available one that has the
// features the caller needs; FromEnvironment reads the name from EXT_ENGINE. The choice is made once,
// when a session or an instance manager is opened, and it is visible: Session.Engine names the engine
// that runs the session. An engine is never replaced by another halfway through a session, and Choose
// never falls back silently: a name that cannot be served is an error.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
)

// Name identifies an engine.
type Name string

const (
	// CGo is the C engine linked through cgo (the ext_cengine build tag and a C library).
	CGo Name = "cgo"
	// C2Go is the C engine translated to Go.
	C2Go Name = "c2go"
	// Go is the pure-Go contract, the oracle.
	Go Name = "go"
)

// Auto asks Choose for the best available engine.
const Auto Name = "auto"

// Environment is the variable FromEnvironment reads: an engine name, or auto (the default).
const Environment = "EXT_ENGINE"

// Feature is something an engine may or may not carry. An engine that is not available has none.
type Feature uint

const (
	// Services is Call: the engine's pure services (descriptor, request, response, frame, manifest,
	// schema and endpoint rules) run by name on bounded JSON. The pure-Go engine has none: its rules
	// are the Go functions of the contract.
	Services Feature = 1 << iota
	// Sessions is Open, OpenProcess and NewInstances.
	Sessions
	// Progress is Session.CallProgress and plugins that declare progress events.
	Progress
	// Reverse is plugin.Options.Reverse and plugins that declare reverse operations.
	Reverse
)

// Has reports whether f holds every feature of want.
func (f Feature) Has(want Feature) bool { return f&want == want }

func (f Feature) String() string {
	var names []string
	for _, n := range []struct {
		f    Feature
		name string
	}{{Services, "services"}, {Sessions, "sessions"}, {Progress, "progress"}, {Reverse, "reverse"}} {
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
	// Open admits a plugin over the backend options.Connect supplies. It needs Sessions.
	Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (Session, error)
	// OpenProcess is Open with a child process the engine itself starts; options.Connect is unused.
	// It needs Sessions.
	OpenProcess(ctx context.Context, d plugin.Descriptor, launch Launch, options plugin.Options) (Session, error)

	// sealed keeps the set closed: NewInstances is generic, which methods cannot be, so it switches on
	// the three engines this package defines.
	sealed()
}

// Session is one admitted connection to a plugin, whichever engine runs it.
type Session interface {
	// Engine names the engine that runs this session.
	Engine() Name
	Descriptor() plugin.Descriptor
	State() plugin.State
	// Done is closed once the session has been closed or aborted.
	Done() <-chan struct{}
	Call(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error)
	// CallProgress is Call for an operation that declares progress. It needs Progress; an engine
	// without it reports plugin.ErrUnsupported.
	CallProgress(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage, onProgress func(plugin.Progress), options ...plugin.CallOptions) (json.RawMessage, error)
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

// NewInstances creates a manager of at most options.Capacity instances on e. It needs Sessions.
func NewInstances[T any](e Engine, options instance.Options[T]) (Instances[T], error) {
	switch e := e.(type) {
	case goEngine:
		return newGoInstances(e, options)
	case cgoEngine:
		return newCGoInstances(e, options)
	case c2goEngine:
		return newC2GoInstances(e, options)
	}
	return nil, fmt.Errorf("engine: %v: %w", e, plugin.ErrUnsupported)
}

// Engines lists every engine, available or not, in the order Choose prefers them: cgo, c2go, go.
func Engines() []Engine { return []Engine{cgoEngine{}, c2goEngine{}, goEngine{}} }

// Get returns the engine called name, which must be one of CGo, C2Go and Go. It does not check that
// the engine is available.
func Get(name Name) (Engine, error) {
	for _, e := range Engines() {
		if e.Name() == name {
			return e, nil
		}
	}
	return nil, fmt.Errorf("engine: unknown engine %q (want %s, %s, %s or %s): %w", name, CGo, C2Go, Go, Auto, plugin.ErrInvalid)
}

// Choose returns the engine to use. With Auto (or an empty name) it is the best available engine,
// in the order cgo, c2go, go, that carries every feature in need. With a name it is that engine,
// and an engine that is not available, or lacks a feature, is an error that wraps
// plugin.ErrUnsupported: asking for an engine by name never gets another one.
func Choose(name Name, need Feature) (Engine, error) {
	if name == "" || name == Auto {
		for _, e := range Engines() {
			if e.Features().Has(need) {
				return e, nil
			}
		}
		return nil, fmt.Errorf("engine: no engine here carries %s: %w", need, plugin.ErrUnsupported)
	}
	e, err := Get(name)
	if err != nil {
		return nil, err
	}
	if !e.Available() {
		return nil, fmt.Errorf("engine: %s is not available in this build: %w", name, plugin.ErrUnsupported)
	}
	if !e.Features().Has(need) {
		return nil, fmt.Errorf("engine: %s lacks %s (it has %s): %w", name, need&^e.Features(), e.Features(), plugin.ErrUnsupported)
	}
	return e, nil
}

// FromEnvironment is Choose with the name taken from EXT_ENGINE: cgo, c2go, go or auto. Unset or empty
// is auto.
func FromEnvironment(need Feature) (Engine, error) {
	return Choose(Name(strings.TrimSpace(os.Getenv(Environment))), need)
}

// errUnsupported is what an engine that lacks a feature reports.
func errUnsupported(e Engine, what string) error {
	return fmt.Errorf("engine: %s does not carry %s here: %w", e.Name(), what, plugin.ErrUnsupported)
}

// errUnsupportedBy is errUnsupported for a session, which knows its engine's name and not the engine.
func errUnsupportedBy(name Name, what string) error {
	return fmt.Errorf("engine: %s does not carry %s here: %w", name, what, plugin.ErrUnsupported)
}
