//go:build ext_cengine && cgo && (darwin || linux || windows)

package packages

import (
	"encoding/json"

	goengine "github.com/webong/ext/pkg/plugin-go/cengine"
	"github.com/webong/ext/pkg/plugin-go/contract/package"
)

// ValidateManifest reports whether m is a valid ext.package/v1 manifest.
func ValidateManifest(m packagekit.Manifest) error {
	return goengine.EngineValue("package.validate", m, nil)
}

// ResolvePackages orders manifests provider-before-consumer, requiring unique
// exact providers and rejecting cycles. It neither executes nor grants access.
func ResolvePackages(manifests []packagekit.Manifest) (packagekit.Plan, error) {
	if manifests == nil {
		manifests = []packagekit.Manifest{}
	}
	var out packagekit.Plan
	err := goengine.EngineValue("package.resolve", manifests, &out)
	return out, err
}

// SelectEntrypoint chooses the named entrypoint for the environment.
func SelectEntrypoint(m packagekit.Manifest, name string, env packagekit.Environment) (packagekit.LaunchSelection, error) {
	var out packagekit.LaunchSelection
	err := goengine.EngineValue("package.select", map[string]any{"manifest": m, "name": name, "environment": map[string]any{"os": env.OS, "arch": env.Arch, "runtimes": env.Runtimes, "sharedVersions": env.SharedVersions}}, &out)
	return out, err
}

// VerifyArtifacts checks the manifest and the files it lists under root, in C.
// The application must protect reviewed files against modification and
// separately establish trust.
func VerifyArtifacts(m packagekit.Manifest, root string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return goengine.VerifyPackage(data, root)
}
