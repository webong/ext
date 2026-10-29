package store

import (
	"fmt"
	"runtime"
	"strings"

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
// because it is installed.
func (s *Store) ResolveEntrypoint(id, contract, version, protocol, reviewedDigest string) (Entrypoint, error) {
	if reviewedDigest == "" {
		return Entrypoint{}, fmt.Errorf("%w: reviewed package digest required", plugin.ErrDenied)
	}
	p, err := s.Load(id)
	if err != nil {
		return Entrypoint{}, err
	}
	if !p.Supports(contract, version) {
		return Entrypoint{}, fmt.Errorf("%w: package does not serve %s/%s", plugin.ErrUnsupported, contract, version)
	}
	digest, err := p.Digest()
	if err != nil {
		return Entrypoint{}, err
	}
	if digest != reviewedDigest {
		return Entrypoint{}, fmt.Errorf("%w: package digest changed", plugin.ErrDenied)
	}
	bestScore := -1
	var best Entrypoint
	for _, entry := range p.Manifest.Entrypoints {
		if entry.Runtime != "argv" || !containsProtocol(entry.Protocols, protocol) || !entryMatchesOS(entry.Name) {
			continue
		}
		for _, artifact := range p.Manifest.Artifacts {
			if artifact.Name != entry.Artifact || artifact.OS != "" && artifact.OS != runtime.GOOS || artifact.Arch != "" && artifact.Arch != runtime.GOARCH {
				continue
			}
			score := 0
			if artifact.OS != "" {
				score += 2
			}
			if artifact.Arch != "" {
				score++
			}
			if strings.HasSuffix(entry.Name, "."+runtime.GOOS) {
				score += 2
			}
			if score <= bestScore {
				continue
			}
			path, err := p.ArtifactPath(artifact.Name)
			if err != nil {
				return Entrypoint{}, err
			}
			best = Entrypoint{Package: p, Name: entry.Name, Artifact: artifact.Name, Path: path, Digest: digest}
			bestScore = score
		}
	}
	if bestScore < 0 {
		return Entrypoint{}, fmt.Errorf("%w: no compatible %s entrypoint", plugin.ErrUnsupported, protocol)
	}
	return best, nil
}

func containsProtocol(protocols []string, want string) bool {
	for _, protocol := range protocols {
		if protocol == want {
			return true
		}
	}
	return false
}

func entryMatchesOS(name string) bool {
	for _, os := range []string{"windows", "linux", "darwin", "freebsd", "android"} {
		if strings.HasSuffix(name, "."+os) {
			return os == runtime.GOOS
		}
	}
	return true
}
