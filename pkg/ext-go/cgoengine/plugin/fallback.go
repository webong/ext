//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	native "github.com/webong/ext/pkg/ext-go/cengine/plugin"
	"github.com/webong/ext/pkg/ext-go/core/plugin/interop"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/guest/author"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/validate/schema"
	"strings"
)

// The default host uses the canonical C engine through purego. Guest authoring
// and the remaining package helpers retain their language-native APIs.

// CEngine reports whether this build runs on the shared C engine.
const CEngine = true

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

// Session is the canonical C host reached through the shared-library driver.
type Session struct{ *native.Session }

func Open(ctx context.Context, d plugin.Descriptor, options plugin.Options) (*Session, error) {
	lib, err := native.Default()
	if err != nil {
		return nil, err
	}
	session, err := lib.Open(ctx, d, options)
	if err != nil {
		return nil, err
	}
	return &Session{session}, nil
}
func OpenProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options) (*Session, error) {
	return openProcess(ctx, d, launch, options, &Session{})
}
func openProcess(ctx context.Context, d plugin.Descriptor, launch LaunchOptions, options plugin.Options, result *Session) (*Session, error) {
	lib, err := native.Default()
	if err != nil {
		return nil, err
	}
	session, err := lib.OpenProcess(ctx, d, native.Launch{Executable: launch.Executable, Arguments: launch.Arguments, Environment: launch.Environment, Directory: launch.Directory, StderrBytes: launch.StderrBytes}, options)
	if err != nil {
		return nil, err
	}
	result.Session = session
	return result, nil
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
