//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package packages

import "github.com/webong/ext/pkg/ext-go/engine/package"

// ValidateManifest reports whether m is a valid ext.package/v1 manifest.
func ValidateManifest(m packagekit.Manifest) error { return m.Validate() }

// ResolvePackages orders manifests provider-before-consumer, requiring unique
// exact providers and rejecting cycles. It neither executes nor grants access.
func ResolvePackages(manifests []packagekit.Manifest) (packagekit.Plan, error) {
	return packagekit.Resolve(manifests)
}

// SelectEntrypoint chooses the named entrypoint for the environment.
func SelectEntrypoint(m packagekit.Manifest, name string, env packagekit.Environment) (packagekit.LaunchSelection, error) {
	return m.SelectEntrypoint(name, env)
}

// VerifyArtifacts checks the manifest and the files it lists under root. The
// application must protect reviewed files against modification and separately
// establish trust.
func VerifyArtifacts(m packagekit.Manifest, root string) error { return m.VerifyArtifacts(root) }
