package ml

import (
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

type ExecutablePackage struct {
	ID, Version, Binary, OutputDir, OS, Arch string
}

// PackageExecutable seals a model.tensor/v1 guest without installing it.
func PackageExecutable(options ExecutablePackage) (packagekit.Manifest, error) {
	return store.SealExecutable(store.ExecutablePackage{
		Descriptor: plugin.Descriptor{
			APIVersion: plugin.APIVersion,
			Identity:   plugin.Identity{ID: options.ID, Version: options.Version},
			Contracts:  []plugin.Contract{{ContractRef: ContractRef, Operations: []plugin.Operation{{Name: "infer", Surface: "action"}}}},
		},
		Protocol: Protocol, Binary: options.Binary, OutputDir: options.OutputDir,
		OS: options.OS, Arch: options.Arch,
	})
}
