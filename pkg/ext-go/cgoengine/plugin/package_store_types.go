package plugin

import "errors"

// ErrPackageExists reports an install over an existing package.
var ErrPackageExists = errors.New("plugin package already installed")

// PackageStoreLimits bounds artifacts, trees and archives; zero selects C defaults.
type PackageStoreLimits struct {
	MaxArtifactBytes int64
	MaxTreeFiles     int
	MaxTreeBytes     int64
	MaxArchiveBytes  int64
}

// PackageEntrypoint is the verified executable selected by contract and protocol.
type PackageEntrypoint struct {
	Name     string `json:"name"`
	Artifact string `json:"artifact"`
	Path     string `json:"path"`
	Digest   string `json:"digest"`
}

// PackageRecord identifies a verified package, its layer and its manifest bytes.
type PackageRecord struct {
	ID        string
	Directory string
	Layer     int
	Manifest  []byte
}

// PackageCheck reports whether an entry below a package root verifies.
type PackageCheck struct {
	ID        string
	Directory string
	Layer     int
	Err       error
}
