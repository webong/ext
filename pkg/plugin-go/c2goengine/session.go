package c2goengine

import (
	"context"

	"github.com/webong/ext/pkg/plugin-go/c2goengine/internal/ccgo"
	plugin "github.com/webong/ext/pkg/plugin-go/engine"
)

// HostAvailable reports whether this build holds the translated host: sessions over Go backends
// and over child processes the engine owns. It is true on the platforms with the host profile (see
// README.md); where it is false, Open and OpenProcess report plugin.ErrUnsupported.
const HostAvailable = ccgo.HostAvailable

type (
	// Session is a plugin session whose engine is the translated C host. It implements
	// author.Caller. Close drains and frees it; Abort stops work and then frees it.
	Session = ccgo.Session
	// ProcessOptions launches a reviewed executable directly, without a shell.
	ProcessOptions = ccgo.ProcessOptions
	// LaunchOptions starts a reviewed executable the way an installed package is run.
	LaunchOptions = ccgo.LaunchOptions
)

// Open runs a plugin over a Go backend (in-process, JSON-line, HashiCorp, WASI, native Go) from
// options.Connect, as cgoengine.Open does, with the engine's session logic running as Go. A plugin
// that declares progress events or reverse operations is refused, and options.Reverse is
// unsupported: neither is carried yet.
func Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (*Session, error) {
	return ccgo.Open(ctx, d, options)
}

// OpenProcess is Open with the child process owned by the engine: it starts the launch, then
// handshakes. options.Connect is unused. The channel to the child is written in Go (os/exec).
func OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	return ccgo.OpenProcess(ctx, d, launch, options)
}
