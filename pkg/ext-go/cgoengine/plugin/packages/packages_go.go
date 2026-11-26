//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package packages

import "github.com/webong/ext/pkg/ext-go/engine/plugin/package"

// ValidateManifest reports whether m is a valid ext.io/v1 manifest.
func ValidateManifest(m packagekit.Manifest) error { return m.Validate() }

// ResolvePackages orders manifests provider-before-consumer, requiring unique
// exact providers and rejecting cycles. It neither executes nor grants access.
func ResolvePackages(manifests []packagekit.Manifest) (packagekit.Plan, error) {
	return packagekit.Resolve(manifests)
}

// ResolvePackagesFor resolves requirements for an explicit operating system and architecture.
func ResolvePackagesFor(manifests []packagekit.Manifest, os, arch string) (packagekit.Plan, error) {
	return packagekit.ResolveFor(manifests, os, arch)
}

// SelectEntrypoint chooses the named entrypoint for the environment.
func SelectEntrypoint(m packagekit.Manifest, name string, env packagekit.Environment) (packagekit.LaunchSelection, error) {
	return m.SelectEntrypoint(name, env)
}

// VerifyArtifacts checks the manifest and the files it lists under root. The
// application must protect reviewed files against modification and separately
// establish trust.
func VerifyArtifacts(m packagekit.Manifest, root string) error { return m.VerifyArtifacts(root) }
