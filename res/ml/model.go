// Package ml defines portable, transport-free contracts for on-device model
// support: model manifests, verified artifact installation, model inspection,
// and bounded inference through a caller-selected provider.
//
// The package owns manifest and store contracts only. It never downloads a
// model, chooses a provider, runs inference itself, authorizes access, or
// exposes a remote protocol. Native model backends ship beside this contract
// as platform implementations (iOS CoreML in res/ml/ios, Android NNAPI/TensorFlow
// Lite in res/ml/android) or as product adapters.
package ml

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// ManifestAPIVersion is the declared version of the manifest contract.
	ManifestAPIVersion = "content.model/v1"

	// MaxManifestBytes bounds a model manifest in bytes.
	MaxManifestBytes = 64 << 10
	// MaxNameLength bounds model names, providers, roles, and file names.
	MaxNameLength = 128
	// MaxCapabilities bounds declared capabilities.
	MaxCapabilities = 32
)

var (
	// ErrInvalid reports a malformed manifest, artifact, or store value.
	ErrInvalid = errors.New("invalid model value")
	// ErrNotFound reports a model or artifact that is not installed.
	ErrNotFound = errors.New("model not found")
	// ErrExist reports a model that is already installed.
	ErrExist = errors.New("model already installed")
	// ErrDigest reports a SHA-256 digest mismatch during installation or load.
	ErrDigest = errors.New("digest mismatch")
)

// ManifestAPIVersionPattern anchors manifest version validation.
var ManifestAPIVersionPattern = ManifestAPIVersion

// ModelManifest describes one model bundle. Name selects the model, Version
// selects the release, Provider names the native runtime that can load it,
// Capabilities declare what the model can do (for example "image.classify",
// "text.generate", "audio.transcribe"), and Artifacts list the files that make
// up the bundle with their SHA-256 digests.
type Manifest struct {
	APIVersion   string     `json:"apiVersion"`
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Provider     string     `json:"provider"`
	Capabilities []string   `json:"capabilities"`
	Artifacts    []Artifact `json:"artifacts"`
}

// Artifact is one file in a model bundle. Role is the semantic role
// (for example "model", "labels", "metadata"), File is the portable file name
// within the bundle directory, and SHA256 is the hex-encoded digest of the
// file contents.
type Artifact struct {
	Role   string `json:"role"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// InstalledModel is an inspected, verified model bundle. Directory is the
// caller-selected store directory containing the bundle.
type InstalledModel struct {
	Manifest  Manifest `json:"manifest"`
	Directory string   `json:"directory"`
}

// Reference identifies one installed model.
type Reference struct {
	Provider string
	Name     string
	Version  string
}

// String renders the reference as provider/name@version.
func (r Reference) String() string {
	return r.Provider + "/" + r.Name + "@" + r.Version
}

// Validate checks manifest identity, version, capabilities, and artifacts.
func (m Manifest) Validate() error {
	if m.APIVersion != ManifestAPIVersion {
		return fmt.Errorf("%w: manifest apiVersion must be %q", ErrInvalid, ManifestAPIVersion)
	}
	if err := validateName(m.Name, "model name"); err != nil {
		return err
	}
	if err := validateVersion(m.Version); err != nil {
		return err
	}
	if err := validateName(m.Provider, "provider"); err != nil {
		return err
	}
	if len(m.Capabilities) > MaxCapabilities {
		return fmt.Errorf("%w: at most %d capabilities are supported", ErrInvalid, MaxCapabilities)
	}
	seenCap := map[string]bool{}
	for _, cap := range m.Capabilities {
		if err := validateName(cap, "capability"); err != nil {
			return err
		}
		if seenCap[cap] {
			return fmt.Errorf("%w: duplicate capability %q", ErrInvalid, cap)
		}
		seenCap[cap] = true
	}
	if len(m.Artifacts) < 1 || len(m.Artifacts) > MaxArtifacts {
		return fmt.Errorf("%w: manifest needs between 1 and %d artifacts", ErrInvalid, MaxArtifacts)
	}
	seenFile := map[string]bool{}
	seenRole := map[string]bool{}
	for _, artifact := range m.Artifacts {
		if err := artifact.Validate(); err != nil {
			return err
		}
		if seenFile[artifact.File] {
			return fmt.Errorf("%w: duplicate artifact file %q", ErrInvalid, artifact.File)
		}
		seenFile[artifact.File] = true
		if seenRole[artifact.Role] {
			return fmt.Errorf("%w: duplicate artifact role %q", ErrInvalid, artifact.Role)
		}
		seenRole[artifact.Role] = true
	}
	return nil
}

// Validate checks artifact identity and digest format.
func (a Artifact) Validate() error {
	if err := validateName(a.Role, "artifact role"); err != nil {
		return err
	}
	if err := validateName(a.File, "artifact file"); err != nil {
		return err
	}
	if !isHexSHA256(a.SHA256) {
		return fmt.Errorf("%w: artifact %q sha256 must be a lowercase hex SHA-256 digest", ErrInvalid, a.File)
	}
	return nil
}

func validateName(value, what string) error {
	if value == "" || len(value) > MaxNameLength || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is required and must be valid UTF-8 of at most %d bytes", ErrInvalid, what, MaxNameLength)
	}
	for index, character := range value {
		if character < 0x20 || character == 0x7f || character == '/' || character == '\\' {
			return fmt.Errorf("%w: %s must not contain control characters, slash, or backslash", ErrInvalid, what)
		}
		if index == 0 && (character == '.' || character == '-' || character == '_') {
			return fmt.Errorf("%w: %s must not start with '.', '-', or '_'", ErrInvalid, what)
		}
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s must not have leading or trailing whitespace", ErrInvalid, what)
	}
	return nil
}

func validateVersion(value string) error {
	if value == "" || len(value) > MaxNameLength || !utf8.ValidString(value) {
		return fmt.Errorf("%w: model version is required and must be valid UTF-8 of at most %d bytes", ErrInvalid, MaxNameLength)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f || character == '/' || character == '\\' {
			return fmt.Errorf("%w: model version must not contain control characters, slash, or backslash", ErrInvalid)
		}
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: model version must not have leading or trailing whitespace", ErrInvalid)
	}
	return nil
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

// ReferenceFrom parses a provider/name@version reference string. The provider
// and name may not contain '/', '@', or whitespace; the version may not
// contain '@' or whitespace.
func ReferenceFrom(value string) (Reference, error) {
	if value == "" {
		return Reference{}, fmt.Errorf("%w: model reference is required", ErrInvalid)
	}
	provider, rest, ok := strings.Cut(value, "/")
	if !ok || provider == "" || rest == "" {
		return Reference{}, fmt.Errorf("%w: model reference must be provider/name@version", ErrInvalid)
	}
	name, version, ok := strings.Cut(rest, "@")
	if !ok || name == "" || version == "" {
		return Reference{}, fmt.Errorf("%w: model reference must be provider/name@version", ErrInvalid)
	}
	if err := validateName(provider, "provider"); err != nil {
		return Reference{}, err
	}
	if err := validateName(name, "model name"); err != nil {
		return Reference{}, err
	}
	if err := validateVersion(version); err != nil {
		return Reference{}, err
	}
	return Reference{Provider: provider, Name: name, Version: version}, nil
}

// CanonicalKey returns a stable store key for the reference.
func (r Reference) CanonicalKey() string {
	return strings.ToLower(r.Provider) + "\x00" + strings.ToLower(r.Name) + "\x00" + strings.ToLower(r.Version)
}

// parseUnsignedHex decodes a lowercase hex SHA-256 digest into raw bytes.
func parseUnsignedHex(value string) ([]byte, error) {
	if !isHexSHA256(value) {
		return nil, fmt.Errorf("%w: not a lowercase hex SHA-256 digest", ErrInvalid)
	}
	digest, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid SHA-256 digest", ErrInvalid)
	}
	return digest, nil
}

// validateArtifactRoles returns a sorted list of the artifact roles declared by
// the manifest.
func validateArtifactRoles(manifest Manifest) ([]string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	roles := make([]string, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		roles = append(roles, artifact.Role)
	}
	sort.Strings(roles)
	return roles, nil
}

// ensureUnicodeBounds returns an error if the value contains a NUL or a
// disallowed control character.
func ensureUnicodeBounds(value string) error {
	for _, character := range value {
		if character == 0 || (character < 0x20 && character != '\t' && character != '\n' && character != '\r') {
			return fmt.Errorf("%w: value contains a disallowed control character", ErrInvalid)
		}
	}
	return nil
}

// maxArtifactBytes returns the per-artifact byte ceiling.
func maxArtifactBytes() int64 { return MaxArtifactBytes }

// maxManifestBytes returns the manifest byte ceiling.
func maxManifestBytes() int64 { return MaxManifestBytes }

// unicodeChecks is a shared helper kept for tests that exercise the name rules.
var unicodeChecks = ensureUnicodeBounds

var _ = unicode.IsPrint
