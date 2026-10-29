package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

// ExecutablePackage describes a compiled guest to seal as an ext.package/v1.
// The descriptor's revision is set to the executable's SHA-256 digest.
type ExecutablePackage struct {
	Descriptor plugin.Descriptor
	Protocol   string
	Binary     string
	OutputDir  string
	OS         string
	Arch       string
}

// SealExecutable publishes a package template without installing or trusting
// it. The caller chooses the protocol and descriptor; no product is selected.
func SealExecutable(options ExecutablePackage) (packagekit.Manifest, error) {
	if options.Binary == "" || options.OutputDir == "" || options.Protocol == "" {
		return packagekit.Manifest{}, plugin.ErrInvalid
	}
	declared := false
	for _, contract := range options.Descriptor.Contracts {
		if contract.Name+"/"+contract.Version == options.Protocol {
			declared = true
			break
		}
	}
	if !declared {
		return packagekit.Manifest{}, plugin.ErrInvalid
	}
	info, err := os.Lstat(options.Binary)
	if err != nil {
		return packagekit.Manifest{}, err
	}
	if !info.Mode().IsRegular() {
		return packagekit.Manifest{}, plugin.ErrInvalid
	}
	if options.OS == "" {
		options.OS = runtime.GOOS
	}
	if options.Arch == "" {
		options.Arch = runtime.GOARCH
	}
	if _, err := os.Lstat(options.OutputDir); err == nil {
		return packagekit.Manifest{}, fmt.Errorf("%w: output directory exists", plugin.ErrInvalid)
	} else if !os.IsNotExist(err) {
		return packagekit.Manifest{}, err
	}
	parent := filepath.Dir(options.OutputDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return packagekit.Manifest{}, err
	}
	staging, err := os.MkdirTemp(parent, ".ext-package-")
	if err != nil {
		return packagekit.Manifest{}, err
	}
	defer os.RemoveAll(staging)
	name := "provider"
	if options.OS == "windows" {
		name += ".exe"
	}
	input, err := os.Open(options.Binary)
	if err != nil {
		return packagekit.Manifest{}, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return packagekit.Manifest{}, plugin.ErrInvalid
	}
	output, err := os.OpenFile(filepath.Join(staging, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return packagekit.Manifest{}, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, DefaultMaxArtifactBytes+1))
	closeErr := output.Close()
	if copyErr != nil {
		return packagekit.Manifest{}, copyErr
	}
	if closeErr != nil {
		return packagekit.Manifest{}, closeErr
	}
	if n > DefaultMaxArtifactBytes {
		return packagekit.Manifest{}, plugin.ErrInvalid
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	options.Descriptor.Identity.Revision = digest
	manifest := packagekit.Manifest{
		APIVersion:  packagekit.Version,
		Descriptor:  options.Descriptor,
		Artifacts:   []packagekit.Artifact{{Name: "main", Path: name, SHA256: digest, OS: options.OS, Arch: options.Arch}},
		Entrypoints: []packagekit.Entrypoint{{Name: "main", Runtime: "argv", Artifact: "main", Protocols: []string{options.Protocol}}},
	}
	if err := WriteManifest(staging, manifest); err != nil {
		return packagekit.Manifest{}, err
	}
	if err := os.Rename(staging, options.OutputDir); err != nil {
		return packagekit.Manifest{}, err
	}
	return manifest, nil
}
