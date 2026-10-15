// Package goengine is the Go entry point to the ext plugin engine. One API, two
// builds: with the ext_cengine tag and cgo (Linux, macOS, Windows with
// MinGW-w64) the contract validation, sessions, guests, package integrity and
// process launch run in the shared C engine in lib/plugin; in any other build
// the same API is served by the pure-Go implementation in pkg/plugin, so
// CGO_ENABLED=0 programs keep working. CEngine reports which build this is.
//
// The data model (descriptors, requests, responses, errors, backends) is
// pkg/plugin's, re-exported here so a program imports this package alone.
// Engine-only APIs (Host, Streams, Instances, NewProcess and the like) need the
// C engine build.
package goengine

import "github.com/webong/ext/pkg/plugin"

// Data model, re-exported from pkg/plugin.
type (
	AdapterInvocation = plugin.AdapterInvocation
	Backend           = plugin.Backend
	BackendProfile    = plugin.BackendProfile
	Contract          = plugin.Contract
	ContractRef       = plugin.ContractRef
	Descriptor        = plugin.Descriptor
	Endpoint          = plugin.Endpoint
	Event             = plugin.Event
	GuestOptions      = plugin.GuestOptions
	Handler           = plugin.Handler
	Identity          = plugin.Identity
	Observer          = plugin.Observer
	Operation         = plugin.Operation
	Options           = plugin.Options
	RemoteError       = plugin.RemoteError
	Request           = plugin.Request
	Requirement       = plugin.Requirement
	Response          = plugin.Response
	State             = plugin.State
)

const (
	APIVersion                = plugin.APIVersion
	AdapterAPIVersion         = plugin.AdapterAPIVersion
	AdapterContractName       = plugin.AdapterContractName
	DefaultTimeout            = plugin.DefaultTimeout
	MaxFrameBytes             = plugin.MaxFrameBytes
	MaxRetryAfterMilliseconds = plugin.MaxRetryAfterMilliseconds
	StateClosed               = plugin.StateClosed
	StateDraining             = plugin.StateDraining
	StateFailed               = plugin.StateFailed
	StateReady                = plugin.StateReady
)

// Error classes. They are the same values as pkg/plugin's, so errors.Is works
// across both packages.
var (
	ErrAmbiguous   = plugin.ErrAmbiguous
	ErrClosed      = plugin.ErrClosed
	ErrDenied      = plugin.ErrDenied
	ErrDraining    = plugin.ErrDraining
	ErrInvalid     = plugin.ErrInvalid
	ErrMismatch    = plugin.ErrMismatch
	ErrNotFound    = plugin.ErrNotFound
	ErrUnsupported = plugin.ErrUnsupported
)

// Contract helpers, re-exported from pkg/plugin.
var (
	AdapterDescriptor      = plugin.AdapterDescriptor
	CheckRequirements      = plugin.CheckRequirements
	Decode                 = plugin.Decode
	MatchHandshake         = plugin.MatchHandshake
	Method                 = plugin.Method
	NegotiateProtocol      = plugin.NegotiateProtocol
	ParseAdapterInvocation = plugin.ParseAdapterInvocation
	PortablePath           = plugin.PortablePath
	Select                 = plugin.Select
	TreeDigest             = plugin.TreeDigest
	ValidateRequest        = plugin.ValidateRequest
)

// ProcessOptions launches a reviewed executable directly, without a shell.
// Environment is explicit and never inherits the embedding process environment.
type ProcessOptions struct {
	Executable             string
	Arguments, Environment []string
}

// LaunchOptions starts a reviewed executable the way an installed package is
// run: like ProcessOptions, but with a working directory, bounded capture of
// the child's stderr and a scrubbed base environment of PATH. The environment
// entries are added to it (an entry named PATH replaces it). Executable must
// be the absolute path of a regular file.
type LaunchOptions struct {
	ProcessOptions
	// Directory is the child's working directory; empty inherits the host's.
	// It must be absolute. The C engine needs a platform with a spawn working
	// directory (macOS, glibc 2.29 or later, Windows).
	Directory string
	// StderrBytes bounds the retained stderr; zero retains 4096 bytes.
	StderrBytes int
}
