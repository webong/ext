// Package wasm defines the protocol-neutral WebAssembly host contract. A host
// chooses an implementation; this package does not select or load a runtime.
package wasm

import (
	"context"
	"errors"
	"io"
	"io/fs"
)

const MaxModuleBytes = 64 << 20
const DefaultMemoryPages = 4096 // 256 MiB of guest linear memory.

var (
	ErrInvalid     = errors.New("wasm: invalid")
	ErrUnsupported = errors.New("wasm: unsupported")
	ErrMismatch    = errors.New("wasm: ABI mismatch")
	ErrClosed      = errors.New("wasm: closed")
)

// CompilationCache is owned by the selected runtime. Share one only among
// sessions of that runtime, then close it after those sessions have closed.
type CompilationCache interface {
	Close(context.Context) error
}

// Options configures a persistent WASI command connected through raw bytes.
type Options struct {
	// MemoryLimitPages bounds guest linear memory. Zero uses DefaultMemoryPages.
	MemoryLimitPages uint32
	// FS is mounted at /. The caller must supply a confined implementation;
	// os.DirFS follows symlinks. Nil grants no filesystem.
	FS   fs.FS
	Args []string
	Env  map[string]string
	// Stderr receives guest diagnostics. Nil discards them.
	Stderr io.Writer
	// MaxStderrBytes caps forwarded diagnostics. Zero defaults to 64 KiB.
	MaxStderrBytes   int64
	CompilationCache CompilationCache
	// StubImports answer a fixed status for declared host functions.
	StubImports []StubImport
}

// Command is one persistent command. Its bytes carry no assigned protocol.
type Command interface {
	io.ReadWriteCloser
	Wait(context.Context) error
}

// CommandOptions describes one run of a WASI command. Nothing is inherited
// from the host unless the caller supplies it here.
type CommandOptions struct {
	Name             string
	Args             []string
	Env              map[string]string
	Dirs             map[string]string
	ReadOnlyDirs     map[string]string
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	MemoryLimitPages uint32
	CompilationCache CompilationCache
	// BeforeInstantiate runs after compilation and configuration, before any
	// guest instruction. An error discards the prepared runtime.
	BeforeInstantiate func() error
}

// Target classifies what a module needs from its host.
type Target string

const (
	TargetNone      Target = "none"
	TargetWASI      Target = "wasi"
	TargetHost      Target = "host"
	TargetComponent Target = "component"
)

// Inspection summarizes a module's imports without running it.
type Inspection struct {
	Target      Target   `json:"target"`
	Imports     []string `json:"imports"`
	Unsupported []string `json:"unsupported,omitempty"`
	Web         bool     `json:"web"`
}

// ReactorABI names the exports of an application-defined request/response
// reactor. Operation and status values belong to the caller's protocol.
type ReactorABI struct {
	Version          uint32
	MaxMessageBytes  uint32
	SuccessStatus    uint32
	VersionExport    string
	OpenExport       string
	CallExport       string
	CloseExport      string
	AllocateExport   string
	FreeExport       string
	InitializeExport string
	DisallowedExport string
	StubImports      []StubImport
}

// StubImport is a host function with Params i32 arguments and one i32 result.
type StubImport struct {
	Module, Name string
	Params       int
	Result       uint32
}

// ReactorOptions configures one reactor instance.
type ReactorOptions struct {
	MemoryLimitPages uint32
	Stderr           io.Writer
	MaxStderrBytes   int64
	CompilationCache CompilationCache
}

// Reactor hosts raw request/response calls. The implementation serializes
// calls to an instance and owns its lifetime.
type Reactor interface {
	Call(context.Context, uint32, []byte) (uint32, []byte, error)
	Close() error
	WaitClosed(context.Context) error
}

// Runtime is the platform implementation a host selects. Inspection may
// report an unsupported module even when a different runtime can execute it.
type Runtime interface {
	NewCompilationCache() CompilationCache
	Open(context.Context, []byte, Options) (Command, error)
	RunCommand(context.Context, []byte, CommandOptions) (int, error)
	Inspect(context.Context, []byte) (Inspection, error)
	OpenReactor(context.Context, []byte, ReactorABI, ReactorOptions) (Reactor, error)
}
