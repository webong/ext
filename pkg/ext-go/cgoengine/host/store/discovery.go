package store

import (
	"fmt"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
)

// Entrypoint is a verified executable selected for one protocol and platform.
type Entrypoint struct {
	Package  Installed
	Name     string
	Artifact string
	Path     string
	Digest   string
}

// ResolveEntrypoint loads a sealed package, checks its exact contract and
// caller-reviewed digest, then selects a compatible executable. The caller
// owns the trust record and supplies its digest; no package is trusted merely
// because it is installed. The selection is made by the C engine's package
// store (or by the pure-Go store behind it in a build without the C engine),
// which applies the rules of engine/host/store: runtime "argv", the protocol,
// an entrypoint named for another operating system skipped, and the artifact
// that names an os and an arch preferred. A failure is the same class of
// error as there (plugin.ErrDenied, ErrUnsupported, ErrNotFound and so on).
func (s *Store) ResolveEntrypoint(id, contract, version, protocol, reviewedDigest string) (Entrypoint, error) {
	if reviewedDigest == "" {
		return Entrypoint{}, fmt.Errorf("%w: reviewed package digest required", plugin.ErrDenied)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.open()
	if err != nil {
		return Entrypoint{}, err
	}
	defer e.Close()
	selected, err := e.ResolveEntrypoint(id, contract, version, protocol, reviewedDigest)
	if err != nil {
		return Entrypoint{}, storeError(err)
	}
	// The package under the same lock, so what is returned is what was selected from.
	manifest, err := e.Load(id)
	if err != nil {
		return Entrypoint{}, storeError(err)
	}
	p, err := s.installed(manifest)
	if err != nil {
		return Entrypoint{}, err
	}
	return Entrypoint{Package: p, Name: selected.Name, Artifact: selected.Artifact, Path: selected.Path, Digest: selected.Digest}, nil
}
