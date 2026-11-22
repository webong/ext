package inference

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

// ExecutablePackage describes one compiled inference guest to package. Empty
// OS and Arch use the current platform. The output directory must not exist.
type ExecutablePackage struct {
	ID, Version, Binary, OutputDir, OS, Arch string
}

// PackageExecutable copies a compiled guest into a sealed ext.package/v1
// directory. The caller reviews and installs the resulting package separately.
func PackageExecutable(options ExecutablePackage) (packagekit.Manifest, error) {
	if options.ID == "" || options.Binary == "" || options.OutputDir == "" {
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
	staging, err := os.MkdirTemp(parent, ".inference-package-")
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
	output, err := os.OpenFile(filepath.Join(staging, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return packagekit.Manifest{}, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	closeErr := output.Close()
	if copyErr != nil {
		return packagekit.Manifest{}, copyErr
	}
	if closeErr != nil {
		return packagekit.Manifest{}, closeErr
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	manifest := packagekit.Manifest{
		APIVersion:  packagekit.Version,
		Descriptor:  Descriptor(Identity{ID: options.ID, Revision: digest, Version: options.Version}),
		Artifacts:   []packagekit.Artifact{{Name: "main", Path: name, SHA256: digest, OS: options.OS, Arch: options.Arch}},
		Entrypoints: []packagekit.Entrypoint{{Name: "main", Runtime: "argv", Artifact: "main", Protocols: []string{ContractName + "/" + ContractVersion}}},
	}
	if err := store.WriteManifest(staging, manifest); err != nil {
		return packagekit.Manifest{}, err
	}
	if err := os.Rename(staging, options.OutputDir); err != nil {
		return packagekit.Manifest{}, err
	}
	return manifest, nil
}
