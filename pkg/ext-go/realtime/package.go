package realtime

import (
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
)

type ExecutablePackage struct{ ID, Version, Binary, OutputDir, OS, Arch string }

func PackageExecutable(options ExecutablePackage) (packagekit.Manifest, error) {
	return store.SealExecutable(store.ExecutablePackage{
		Descriptor: Descriptor(plugin.Identity{ID: options.ID, Version: options.Version}),
		Protocol:   Protocol, Binary: options.Binary, OutputDir: options.OutputDir,
		OS: options.OS, Arch: options.Arch,
	})
}
