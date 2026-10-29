package store

import (
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	reference "github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

type ExecutablePackage struct {
	Descriptor plugin.Descriptor
	Protocol   string
	Binary     string
	OutputDir  string
	OS         string
	Arch       string
}

// SealExecutable shares the same package authoring helper in both Go store
// builds. Installation and verification still use this store's engine route.
func SealExecutable(options ExecutablePackage) (packagekit.Manifest, error) {
	return reference.SealExecutable(reference.ExecutablePackage(options))
}
