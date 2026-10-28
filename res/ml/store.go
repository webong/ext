// Package ml store implements bounded, caller-selected model artifact
// installation. A Store is rooted at a directory the caller selects and
// authorizes; the library never picks the path and never deletes anything the
// caller did not explicitly name.
package ml

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// DefaultMaxArtifacts bounds the number of artifacts a model may declare.
	DefaultMaxArtifacts = 64
	// MaxArtifacts is the library ceiling for DefaultMaxArtifacts.
	MaxArtifacts = 256
	// DefaultMaxArtifactBytes bounds one artifact file in bytes.
	DefaultMaxArtifactBytes = 64 << 30
	// MaxArtifactBytes is the library ceiling for DefaultMaxArtifactBytes.
	MaxArtifactBytes = 1 << 40
	// DefaultMaxTotalBytes bounds the total bundle size in bytes.
	DefaultMaxTotalBytes int64 = 1 << 40
	// MaxTotalBytes is the library ceiling for DefaultMaxTotalBytes.
	MaxTotalBytes int64 = 1 << 50
)

// StoreLimits bound model installation. Zero values select the defaults.
type StoreLimits struct {
	MaxArtifacts     int
	MaxArtifactBytes int64
	MaxTotalBytes    int64
}

func (l StoreLimits) normalize() (StoreLimits, error) {
	if l.MaxArtifacts == 0 {
		l.MaxArtifacts = DefaultMaxArtifacts
	}
	if l.MaxArtifactBytes == 0 {
		l.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if l.MaxArtifacts < 1 || l.MaxArtifacts > MaxArtifacts {
		return l, fmt.Errorf("%w: artifact bound must be between 1 and %d", ErrInvalid, MaxArtifacts)
	}
	if l.MaxArtifactBytes < 1 || l.MaxArtifactBytes > MaxArtifactBytes {
		return l, fmt.Errorf("%w: artifact byte bound must be between 1 and %d", ErrInvalid, MaxArtifactBytes)
	}
	if l.MaxTotalBytes < 1 || l.MaxTotalBytes > MaxTotalBytes {
		return l, fmt.Errorf("%w: total byte bound must be between 1 and %d", ErrInvalid, MaxTotalBytes)
	}
	return l, nil
}

// Store manages one caller-selected model root directory. Bundles are stored
// under <root>/<provider>/<name>/<version>/ with a manifest.json and artifact
// files. The caller selects and authorizes the root; the library never picks it.
type Store struct {
	root *os.Root
	path string
}

// ArtifactSource is a bounded, readable source for one model artifact. It is
// the primary installation API: callers supply streaming sources so that large
// artifacts can be hashed and staged without ever being held entirely in
// memory.
type ArtifactSource interface {
	// Open returns a readable stream for the artifact. The caller must close
	// the returned reader. Open is called once per artifact.
	Open(ctx context.Context) (io.ReadCloser, error)
	// Size returns the declared byte size of the artifact, or -1 if unknown.
	Size() int64
}

// InMemoryArtifactSource wraps an in-memory byte slice as an ArtifactSource.
// It is a convenience wrapper for small artifacts; large artifacts should use
// a streaming source that reads from disk or network.
type InMemoryArtifactSource struct {
	data []byte
	size int64
}

// NewInMemoryArtifactSource wraps data as an ArtifactSource.
func NewInMemoryArtifactSource(data []byte) *InMemoryArtifactSource {
	return &InMemoryArtifactSource{data: data, size: int64(len(data))}
}

func (s *InMemoryArtifactSource) Open(ctx context.Context) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func (s *InMemoryArtifactSource) Size() int64 { return s.size }

// OpenStore opens an existing caller-selected model root directory. It refuses
// non-directories, symlinks, and broadly writable directories.
func OpenStore(root string) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("%w: model root must be an absolute path", ErrInvalid)
	}
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("%w: model root path is invalid", ErrInvalid)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("open model root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: model root must be an existing non-symlink directory", ErrInvalid)
	}
	if os.PathSeparator == '/' && info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%w: model root must not be writable by other users", ErrInvalid)
	}
	opened, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open model root: %w", err)
	}
	stat, err := opened.Stat(".")
	if err != nil || !os.SameFile(info, stat) {
		opened.Close()
		return nil, fmt.Errorf("%w: model root changed during inspection", ErrInvalid)
	}
	return &Store{root: opened, path: absolute}, nil
}

// Path returns the absolute model root path.
func (s *Store) Path() string { return s.path }

// Install stages and installs one model bundle from streaming artifact
// sources. It never replaces an existing bundle: an identical manifest is
// reported as already installed; a different one fails with ErrExist.
//
// Each artifact is streamed through a hash and size check as it is copied into
// the staging directory, so large bundles never need to be held entirely in
// memory. If any check fails, the staging directory is removed and no partial
// bundle is visible at the target path.
func (s *Store) Install(ctx context.Context, manifest Manifest, files map[string]ArtifactSource) (*InstalledModel, error) {
	limits, err := (StoreLimits{}).normalize()
	if err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(files) != len(manifest.Artifacts) {
		return nil, fmt.Errorf("%w: manifest declares %d artifacts but %d sources were provided", ErrInvalid, len(manifest.Artifacts), len(files))
	}
	reference := Reference{Provider: manifest.Provider, Name: manifest.Name, Version: manifest.Version}
	targetDir := filepath.Join(s.path, reference.Provider, reference.Name, reference.Version)
	if err := s.rejectUnsafePath(targetDir); err != nil {
		return nil, err
	}
	// Detect an existing bundle before staging.
	if installed, lookupErr := s.resolveLocked(ctx, reference); lookupErr == nil {
		if installed.Manifest.APIVersion == manifest.APIVersion &&
			installed.Manifest.Name == manifest.Name &&
			installed.Manifest.Version == manifest.Version &&
			installed.Manifest.Provider == manifest.Provider &&
			sameCapabilities(installed.Manifest.Capabilities, manifest.Capabilities) &&
			sameArtifacts(installed.Manifest.Artifacts, manifest.Artifacts) {
			return installed, nil
		}
		return nil, ErrExist
	} else if !errors.Is(lookupErr, ErrNotFound) {
		return nil, lookupErr
	}
	// Stage into a temporary sibling directory, hash and size-check every
	// artifact while streaming, then atomically rename into place.
	var total int64
	temporary := ".ml-install-" + randText()
	if err := s.root.Mkdir(temporary, 0o700); err != nil {
		return nil, fmt.Errorf("stage model bundle: %w", err)
	}
	defer s.root.Remove(temporary)
	stage, err := s.root.OpenRoot(temporary)
	if err != nil {
		return nil, fmt.Errorf("stage model bundle: %w", err)
	}
	defer stage.Close()
	for _, artifact := range manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source, ok := files[artifact.File]
		if !ok {
			return nil, fmt.Errorf("%w: artifact file %q is missing", ErrInvalid, artifact.File)
		}
		if size := source.Size(); size >= 0 && size > limits.MaxArtifactBytes {
			return nil, fmt.Errorf("%w: artifact %q exceeds the artifact byte bound of %d", ErrInvalid, artifact.File, limits.MaxArtifactBytes)
		}
		digest, err := parseUnsignedHex(artifact.SHA256)
		if err != nil {
			return nil, err
		}
		reader, err := source.Open(ctx)
		if err != nil {
			return nil, fmt.Errorf("open artifact source %q: %w", artifact.File, err)
		}
		// Create the staged file and stream through a TeeReader that hashes
	 // every byte as it is written. A LimitReader enforces the per-artifact
		// byte bound without buffering the whole artifact.
		out, err := stage.Create(artifact.File)
		if err != nil {
			_ = reader.Close()
			return nil, fmt.Errorf("create staged artifact %q: %w", artifact.File, err)
		}
		hw := &hashWriter{hash: sha256.New()}
		tee := io.TeeReader(io.LimitReader(reader, limits.MaxArtifactBytes+1), hw)
		if _, err := io.Copy(out, tee); err != nil {
			_ = out.Close()
			_ = reader.Close()
			return nil, fmt.Errorf("stream artifact %q: %w", artifact.File, err)
		}
		if err := out.Close(); err != nil {
			_ = reader.Close()
			return nil, fmt.Errorf("close staged artifact %q: %w", artifact.File, err)
		}
		_ = reader.Close()
		// If LimitReader stopped early, the artifact is too large.
		if hw.bytes > limits.MaxArtifactBytes {
			return nil, fmt.Errorf("%w: artifact %q exceeds the artifact byte bound of %d", ErrInvalid, artifact.File, limits.MaxArtifactBytes)
		}
		total += hw.bytes
		if total > limits.MaxTotalBytes {
			return nil, fmt.Errorf("%w: model bundle exceeds the total byte bound of %d", ErrInvalid, limits.MaxTotalBytes)
		}
		sum := hw.finish()
		if !bytesEqual(digest, sum[:]) {
			return nil, fmt.Errorf("%w: artifact %q digest does not match manifest", ErrDigest, artifact.File)
		}
	}
	manifestBytes, err := marshalManifest(manifest)
	if err != nil {
		return nil, err
	}
	if err := stage.WriteFile("manifest.json", manifestBytes, 0o600); err != nil {
		return nil, fmt.Errorf("stage model bundle: %w", err)
	}
	// Atomic rename into place.
	relative := filepath.Join(reference.Provider, reference.Name, reference.Version)
	if err := s.root.MkdirAll(filepath.Dir(relative), 0o700); err != nil {
		return nil, fmt.Errorf("create model bundle directory: %w", err)
	}
	if err := s.root.Rename(temporary, relative); err != nil {
		return nil, fmt.Errorf("publish model bundle: %w", err)
	}
	return &InstalledModel{
		Manifest:  manifest,
		Directory: filepath.Join(s.path, relative),
	}, nil
}

// InstallFromBytes is a convenience wrapper around Install for small artifacts
// that are already in memory. It delegates to Install by wrapping each byte
// slice in an InMemoryArtifactSource.
func (s *Store) InstallFromBytes(ctx context.Context, manifest Manifest, files map[string][]byte) (*InstalledModel, error) {
	sources := make(map[string]ArtifactSource, len(files))
	for name, data := range files {
		sources[name] = NewInMemoryArtifactSource(data)
	}
	return s.Install(ctx, manifest, sources)
}

// hashWriter writes bytes to a staging file while computing their SHA-256
// digest and tracking the byte count.
type hashWriter struct {
	hash  hash.Hash
	sum   [sha256.Size]byte
	bytes int64
}

func (w *hashWriter) Write(p []byte) (int, error) {
	n, err := w.hash.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *hashWriter) finish() [sha256.Size]byte {
	copy(w.sum[:], w.hash.Sum(nil))
	return w.sum
}

// Resolve loads one installed model and re-verifies every artifact digest
// against its manifest before returning. It fails closed on tampering.
func (s *Store) Resolve(ctx context.Context, provider, name, version string) (*InstalledModel, error) {
	reference, err := ReferenceFrom(provider + "/" + name + "@" + version)
	if err != nil {
		return nil, err
	}
	return s.resolveLocked(ctx, reference)
}

// List returns every installed model, with digests re-verified.
func (s *Store) List(ctx context.Context) ([]InstalledModel, error) {
	providers, err := fs.ReadDir(s.root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	var result []InstalledModel
	for _, providerEntry := range providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !providerEntry.IsDir() {
			continue
		}
		providerDir, err := s.root.OpenRoot(providerEntry.Name())
		if err != nil {
			continue
		}
		names, err := fs.ReadDir(providerDir.FS(), ".")
		if err != nil {
			providerDir.Close()
			continue
		}
		for _, nameEntry := range names {
			if err := ctx.Err(); err != nil {
				providerDir.Close()
				return nil, err
			}
			if !nameEntry.IsDir() {
				continue
			}
			nameDir, err := providerDir.OpenRoot(nameEntry.Name())
			if err != nil {
				continue
			}
			versions, err := fs.ReadDir(nameDir.FS(), ".")
			if err != nil {
				nameDir.Close()
				continue
			}
			for _, versionEntry := range versions {
				if err := ctx.Err(); err != nil {
					nameDir.Close()
					providerDir.Close()
					return nil, err
				}
				if !versionEntry.IsDir() {
					continue
				}
				reference := Reference{Provider: providerEntry.Name(), Name: nameEntry.Name(), Version: versionEntry.Name()}
				installed, lookupErr := s.resolveLocked(ctx, reference)
				if lookupErr == nil {
					result = append(result, *installed)
				}
			}
			nameDir.Close()
		}
		providerDir.Close()
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Manifest.Provider+"/"+result[i].Manifest.Name+"@"+result[i].Manifest.Version <
			result[j].Manifest.Provider+"/"+result[j].Manifest.Name+"@"+result[j].Manifest.Version
	})
	return result, nil
}

// Remove deletes one installed model bundle and prunes empty provider and name
// directories. It refuses links and non-directories.
func (s *Store) Remove(ctx context.Context, provider, name, version string) error {
	reference, err := ReferenceFrom(provider + "/" + name + "@" + version)
	if err != nil {
		return err
	}
	relative := filepath.Join(reference.Provider, reference.Name, reference.Version)
	targetDir := filepath.Join(s.path, relative)
	if err := s.rejectUnsafePath(targetDir); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := s.root.Lstat(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove model bundle: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: model bundle is not a directory", ErrInvalid)
	}
	if err := s.root.RemoveAll(relative); err != nil {
		return fmt.Errorf("remove model bundle: %w", err)
	}
	// Prune empty name and provider directories.
	nameDir := filepath.Join(reference.Provider, reference.Name)
	providerDir := reference.Provider
	for _, dir := range []string{nameDir, providerDir} {
		entries, listErr := fs.ReadDir(s.root.FS(), dir)
		if listErr != nil || len(entries) != 0 {
			continue
		}
		_ = s.root.Remove(dir)
	}
	return nil
}

// resolveLocked inspects one installed bundle and re-verifies every artifact
// digest against its manifest.
func (s *Store) resolveLocked(ctx context.Context, reference Reference) (*InstalledModel, error) {
	relative := filepath.Join(reference.Provider, reference.Name, reference.Version)
	targetDir := filepath.Join(s.path, relative)
	manifestBytes, err := s.root.ReadFile(filepath.Join(relative, "manifest.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read model manifest: %w", err)
	}
	if len(manifestBytes) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: model manifest exceeds %d bytes", ErrInvalid, MaxManifestBytes)
	}
	manifest, err := unmarshalManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	if manifest.Provider != reference.Provider || manifest.Name != reference.Name || manifest.Version != reference.Version {
		return nil, fmt.Errorf("%w: manifest identity does not match reference", ErrInvalid)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	for _, artifact := range manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := s.root.ReadFile(filepath.Join(relative, artifact.File))
		if err != nil {
			return nil, fmt.Errorf("read model artifact %q: %w", artifact.File, err)
		}
		digest, err := parseUnsignedHex(artifact.SHA256)
		if err != nil {
			return nil, err
		}
		actual := sha256.Sum256(data)
		if !bytesEqual(digest, actual[:]) {
			return nil, fmt.Errorf("%w: artifact %q digest does not match manifest", ErrDigest, artifact.File)
		}
	}
	return &InstalledModel{
		Manifest:  manifest,
		Directory: targetDir,
	}, nil
}

// rejectUnsafePath rejects store paths that are not relative children of the
// model root or that would traverse outside it.
func (s *Store) rejectUnsafePath(path string) error {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("%w: model bundle path is invalid", ErrInvalid)
	}
	if absolute != s.path && !strings.HasPrefix(absolute, s.path+string(filepath.Separator)) {
		return fmt.Errorf("%w: model bundle path escapes the model root", ErrInvalid)
	}
	relative, err := filepath.Rel(s.path, absolute)
	if err != nil {
		return fmt.Errorf("%w: model bundle path cannot be resolved", ErrInvalid)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: model bundle path escapes the model root", ErrInvalid)
	}
	return nil
}

// sameCapabilities reports whether two capability slices contain the same
// elements, ignoring order.
func sameCapabilities(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, cap := range a {
		seen[cap] = true
	}
	for _, cap := range b {
		if !seen[cap] {
			return false
		}
	}
	return true
}

// sameArtifacts reports whether two artifact slices contain the same files and
// digests, ignoring order.
func sameArtifacts(a, b []Artifact) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]Artifact{}
	for _, artifact := range a {
		seen[artifact.File] = artifact
	}
	for _, artifact := range b {
		existing, ok := seen[artifact.File]
		if !ok || existing.SHA256 != artifact.SHA256 || existing.Role != artifact.Role {
			return false
		}
	}
	return true
}

// bytesEqual reports whether two byte slices are equal.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// marshalManifest writes a compact, deterministic manifest JSON.
func marshalManifest(manifest Manifest) ([]byte, error) {
	manifest, err := normalizeManifest(manifest)
	if err != nil {
		return nil, err
	}
	return json.Marshal(manifest)
}

// normalizeManifest returns a canonicalized copy of the manifest with
// deduplicated, sorted capabilities and artifacts.
func normalizeManifest(manifest Manifest) (Manifest, error) {
	if err := manifest.Validate(); err != nil {
		return manifest, err
	}
	capabilities := make([]string, 0, len(manifest.Capabilities))
	seenCap := map[string]bool{}
	for _, cap := range manifest.Capabilities {
		if !seenCap[cap] {
			seenCap[cap] = true
			capabilities = append(capabilities, cap)
		}
	}
	sort.Strings(capabilities)
	artifacts := make([]Artifact, 0, len(manifest.Artifacts))
	seenFile := map[string]bool{}
	for _, artifact := range manifest.Artifacts {
		if !seenFile[artifact.File] {
			seenFile[artifact.File] = true
			artifacts = append(artifacts, artifact)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].File < artifacts[j].File })
	manifest.Capabilities = capabilities
	manifest.Artifacts = artifacts
	return manifest, nil
}

// unmarshalManifest parses a manifest JSON document.
func unmarshalManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: invalid manifest JSON: %v", ErrInvalid, err)
	}
	return manifest, nil
}

// randText returns a hex-encoded random string for staging directory names.
func randText() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer[:])
}

// compile-time interface checks.
var (
	_ = StoreLimits{}.normalize
	_ = Reference.String
	_ = Reference.CanonicalKey
)
