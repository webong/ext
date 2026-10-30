package perception

import (
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

type ExecutablePackage struct {
	ID, Version, Binary, OutputDir, OS, Arch string
}

// PackageExecutable seals a provider for the current or selected platform.
// The descriptor declares the complete surface; runtime capabilities still
// decide which optional operations the provider serves.
func PackageExecutable(options ExecutablePackage) (packagekit.Manifest, error) {
	return store.SealExecutable(store.ExecutablePackage{
		Descriptor: Descriptor(plugin.Identity{ID: options.ID, Version: options.Version}),
		Protocol:   Protocol, Binary: options.Binary, OutputDir: options.OutputDir,
		OS: options.OS, Arch: options.Arch,
	})
}
