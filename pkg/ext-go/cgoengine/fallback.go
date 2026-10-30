//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package cgoengine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/host/interop"
	"github.com/webong/ext/pkg/ext-go/engine/host/process"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
	"github.com/webong/ext/pkg/ext-go/engine/validate/schema"
	"strings"
)

// This file is the pure-Go build of the engine API: the same functions and
// behavior as the C engine build, served by pkg/ext-go, for programs built
// without cgo, without the ext_cengine tag, or for a platform the C engine does
// not support.

// CEngine reports whether this build runs on the shared C engine.
const CEngine = false

// PackageStore is the package store of the pure-Go engine in this build; the C engine build serves
// the same methods from the C store.
type PackageStore = goPackageStore

// OpenPackageStore returns a store rooted at root.
func OpenPackageStore(root string, limits PackageStoreLimits) (*PackageStore, error) {
	return openGoPackageStore(root, limits)
}

// StoreDigest verifies one installed package using the pure-Go reference.
func StoreDigest(root, id string) (string, error) {
	s, err := store.New(root, store.Limits{})
	if err != nil {
		return "", err
	}
	p, err := s.Load(id)
	if err != nil {
		return "", err
	}
	return p.Digest()
}

func TrustStateOf(path, scope, id, digest string) (hosttrust.State, error) {
	t, err := hosttrust.New(path)
	if err != nil {
		return hosttrust.Untrusted, err
	}
	return t.StateOf(scope, id, digest)
}
func TrustRecord(path, scope, id, digest string) error {
	t, err := hosttrust.New(path)
	if err != nil {
		return err
	}
	return t.Record(scope, id, digest)
}
func TrustRevoke(path, scope, id string) error {
	t, err := hosttrust.New(path)
	if err != nil {
		return err
	}
	return t.Revoke(scope, id)
}

// Guest serves a descriptor and handler. Destroy is a no-op in this build.
type Guest struct{ *plugin.Guest }

func NewGuest(d plugin.Descriptor, options plugin.GuestOptions) (*Guest, error) {
	g, err := plugin.NewGuest(d, options)
	if err != nil {
		return nil, err
	}
	return &Guest{g}, nil
}

// Destroy releases the guest. There is nothing native to release in this build.
func (*Guest) Destroy() {}

// NewRegistry returns a typed authoring registry.
func NewRegistry(identity plugin.Identity) (*author.Registry, error) { return author.New(identity) }

// GuestFromRegistry builds a guest from a registry's descriptor and handlers.
func GuestFromRegistry(r *author.Registry, opts author.Options) (*Guest, error) {
	g, err := r.Guest(opts)
	if err != nil {
		return nil, err
	}
	return &Guest{g}, nil
}

// Session is one admitted connection to a plugin: handshake, verification,
// authorization, calls and draining.
type Session struct {
	*plugin.Session
	proc *process.Process
}

// Open verifies, connects and handshakes with the plugin options supplies.
func Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (*Session, error) {
	s, err := plugin.Open(ctx, d, options)
	if err != nil {
		return nil, err
	}
	return &Session{Session: s}, nil
}

// OpenProcess starts the launch as a child process and opens a session over its
// stdin and stdout. options.Connect is unused.
func OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	if launch.StderrBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	result := &Session{}
	connect := options
	connect.Connect = func(ctx context.Context, _ plugin.Descriptor) (plugin.Backend, error) {
		proc, err := process.Start(ctx, process.Command{
			Path:        launch.Executable,
			Args:        launch.Arguments,
			Dir:         launch.Directory,
			Env:         launch.Environment,
			StderrBytes: launch.StderrBytes,
		})
		if err != nil {
			return nil, err
		}
		result.proc = proc
		return proc, nil
	}
	s, err := plugin.Open(ctx, d, connect)
	if err != nil {
		return nil, err
	}
	result.Session = s
	return result, nil
}

// Stderr returns the retained, bounded and possibly truncated stderr of a
// session opened with OpenProcess. Treat it as untrusted text.
func (s *Session) Stderr() string {
	if s.proc == nil {
		return ""
	}
	return s.proc.Stderr()
}

// SHA256 returns the SHA-256 digest of input.
func SHA256(input []byte) [32]byte { return sha256.Sum256(input) }

func ResolveRoute(hosts, guests []plugin.BackendProfile, bridges []interop.Bridge, w interop.Requirements) (interop.Route, error) {
	return interop.Resolve(hosts, guests, bridges, w)
}
func ValidateSchema(s schema.Schema) error { return s.Validate() }
func CheckSchema(s schema.Schema, value json.RawMessage) error {
	if len(value) == 0 {
		return plugin.ErrInvalid
	}
	return s.Check(value)
}

// DirectoryDigest is the digest of every regular file under root.
func DirectoryDigest(root string) (string, error) {
	if strings.IndexByte(root, 0) >= 0 {
		return "", plugin.ErrInvalid
	}
	return plugin.DirectoryDigest(root)
}

// ArtifactDigest returns the digest of one artifact under root, addressed by a
// portable relative path, never following symbolic links: the SHA-256 of a
// file, or for a tree the directory digest of a directory that holds at least
// one file.
func ArtifactDigest(root, path string, tree bool) (string, error) {
	return plugin.ArtifactDigest(root, path, tree)
}
