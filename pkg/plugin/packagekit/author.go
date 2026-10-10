package packagekit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/webong/ext/pkg/plugin"
)

// MaxAuthoredArtifactBytes bounds one file read while authoring a manifest.
const MaxAuthoredArtifactBytes = 128 << 20

// ExecutableRuntime is the entrypoint runtime ExecutableManifest declares; the
// process backend's Profile uses the same name.
const ExecutableRuntime = "process"

// Name is the package identity ID.
func (m Manifest) Name() string { return m.Descriptor.Identity.ID }

// Version is the package release version, which may be empty.
func (m Manifest) Version() string { return m.Descriptor.Identity.Version }

// Supports reports whether the package serves an exact contract version.
func (m Manifest) Supports(name, version string) bool {
	for _, c := range m.Descriptor.Contracts {
		if c.Name == name && c.Version == version {
			return true
		}
	}
	return false
}

// FileDigest returns the SHA-256 (hex) of a regular file of at most
// MaxAuthoredArtifactBytes.
func FileDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a regular file", plugin.ErrInvalid, filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, MaxAuthoredArtifactBytes+1))
	if err != nil {
		return "", err
	}
	if n > MaxAuthoredArtifactBytes {
		return "", fmt.Errorf("%w: %s exceeds %d MiB", plugin.ErrInvalid, filepath.Base(path), MaxAuthoredArtifactBytes>>20)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ExecutableManifest describes a one-executable package. The executable sits
// beside package.json under its base name, and the artifact is bound to the
// running GOOS and GOARCH. The revision is "sha256:<digest of the executable>",
// so any rebuild is a new immutable revision. The entrypoint "main" runs under
// the process runtime and speaks plugin.APIVersion over JSON lines.
func ExecutableManifest(id, version string, contracts []plugin.Contract, executable string) (Manifest, error) {
	digest, err := FileDigest(executable)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		APIVersion: Version,
		Descriptor: plugin.Descriptor{
			APIVersion: plugin.APIVersion,
			Identity:   plugin.Identity{ID: id, Revision: "sha256:" + digest, Version: version},
			Contracts:  cloneContracts(contracts),
		},
		Artifacts:   []Artifact{{Name: "executable", Path: filepath.Base(executable), SHA256: digest, OS: runtime.GOOS, Arch: runtime.GOARCH}},
		Entrypoints: []Entrypoint{{Name: "main", Runtime: ExecutableRuntime, Artifact: "executable", Protocols: []string{plugin.APIVersion}}},
	}
	return m, m.Validate()
}

// SourceManifest describes a package of source files that the consumer runs
// under a runtime it owns; it declares no entrypoint. files are slash-separated
// paths relative to root, each listed once as an artifact whose name equals its
// path. The revision is "sha256:" over the sorted "path digest" pairs, so it
// follows the files' content and names, not the order given.
func SourceManifest(id, version string, contracts []plugin.Contract, root string, files []string) (Manifest, error) {
	if len(files) == 0 {
		return Manifest{}, fmt.Errorf("%w: a source package needs at least one file", plugin.ErrInvalid)
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	artifacts := make([]Artifact, 0, len(sorted))
	combined := sha256.New()
	for i, file := range sorted {
		if i > 0 && file == sorted[i-1] {
			return Manifest{}, fmt.Errorf("%w: file %q is listed twice", plugin.ErrInvalid, file)
		}
		if !portablePath(file) {
			return Manifest{}, fmt.Errorf("%w: file path %q is not portable", plugin.ErrInvalid, file)
		}
		digest, err := FileDigest(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return Manifest{}, err
		}
		fmt.Fprintf(combined, "%s %s\n", file, digest)
		artifacts = append(artifacts, Artifact{Name: file, Path: file, SHA256: digest})
	}
	m := Manifest{
		APIVersion: Version,
		Descriptor: plugin.Descriptor{
			APIVersion: plugin.APIVersion,
			Identity:   plugin.Identity{ID: id, Revision: "sha256:" + hex.EncodeToString(combined.Sum(nil)), Version: version},
			Contracts:  cloneContracts(contracts),
		},
		Artifacts: artifacts,
	}
	return m, m.Validate()
}

func cloneContracts(contracts []plugin.Contract) []plugin.Contract {
	return plugin.Descriptor{Contracts: contracts}.Clone().Contracts
}

// LoadDescriptor reads a descriptor from a package manifest (ext.package/v1) or
// from a file holding just a descriptor, bounded by plugin.MaxFrameBytes.
func LoadDescriptor(path string) (plugin.Descriptor, error) {
	data, err := readBounded(path)
	if err != nil {
		return plugin.Descriptor{}, err
	}
	if m, err := Decode(data); err == nil {
		return m.Descriptor.Clone(), nil
	}
	var d plugin.Descriptor
	if err := plugin.Decode(data, &d); err != nil {
		return plugin.Descriptor{}, err
	}
	return d, d.Validate()
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, plugin.MaxFrameBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > plugin.MaxFrameBytes {
		return nil, errors.New("file exceeds the plugin frame limit")
	}
	return data, nil
}
