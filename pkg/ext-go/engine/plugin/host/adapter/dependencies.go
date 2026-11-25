package adapter

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// ProjectDependencies adds ephemeral generic edges for legacy adapter metadata.
// Callers verify package bytes first, then pass the returned graph to the engine
// resolver. No manifest is persisted, no approval is granted, and provider
// revisions remain unpinned so compatible upgrades retain the same named edge.
// Adapter-specific name/API/share-space constraints stay owned by this package.
func ProjectDependencies(manifests []packagekit.Manifest, goos string) ([]packagekit.Manifest, error) {
	return ProjectDependenciesFor(manifests, Target{OS: goos, Arch: runtime.GOARCH})
}

// ProjectDependenciesFor checks adapter share-space obligations for a bundle or
// host target and projects only legacy requirements missing from its graph.
func ProjectDependenciesFor(manifests []packagekit.Manifest, target Target) ([]packagekit.Manifest, error) {
	goos, arch := target.OS, target.Arch
	if !validTarget.MatchString(goos) || !validTarget.MatchString(arch) {
		return nil, fmt.Errorf("%w: adapter dependency target requires an operating system and architecture", plugin.ErrInvalid)
	}
	adapters := map[string]*Adapter{}
	dependencies := map[string]map[string]Dependency{}
	for _, pm := range manifests {
		var contract *plugin.Contract
		for i := range pm.Descriptor.Contracts {
			if pm.Descriptor.Contracts[i].Name == plugin.AdapterContractName {
				if contract != nil {
					return nil, fmt.Errorf("%w: multiple adapter contracts for %s", plugin.ErrInvalid, pm.Name())
				}
				contract = &pm.Descriptor.Contracts[i]
			}
		}
		if contract == nil {
			continue
		}
		values := map[string]string{}
		if raw, ok := pm.Metadata[MetadataKey]; ok {
			if err := metadataValues(raw, values); err != nil {
				return nil, err
			}
		}
		id := pm.Descriptor.Identity.ID
		if _, ok := adapters[id]; ok {
			return nil, fmt.Errorf("%w: duplicate adapter %s", plugin.ErrAmbiguous, id)
		}
		capabilities := make([]string, 0, len(contract.Operations))
		for _, op := range contract.Operations {
			capabilities = append(capabilities, op.Name)
		}
		spaces := splitList(values["share_spaces"])
		if contains(capabilities, "share") && len(spaces) == 0 {
			spaces = []string{id}
		}
		adapters[id] = &Adapter{Package: pm, Manifest: Manifest{Name: id, APIVersion: contract.Version, Capabilities: capabilities, ShareSpaces: spaces}}
		deps, err := declaredDependencies(pm, values, goos, arch)
		if err != nil {
			return nil, err
		}
		dependencies[id] = deps
	}
	result := make([]packagekit.Manifest, len(manifests))
	copy(result, manifests)
	for i := range result {
		pm := &result[i]
		deps := dependencies[pm.Descriptor.Identity.ID]
		if len(deps) == 0 {
			continue
		}
		pm.Requires = append([]packagekit.Dependency(nil), pm.Requires...)
		spaces := make([]string, 0, len(deps))
		for space := range deps {
			spaces = append(spaces, space)
		}
		sort.Strings(spaces)
		for _, space := range spaces {
			dep := deps[space]
			target := adapters[dep.Adapter]
			if target == nil {
				return nil, fmt.Errorf("%w: adapter %s requires %s:%s@%s", plugin.ErrNotFound, pm.Name(), dep.Space, dep.Adapter, dep.APIVersion)
			}
			if err := ValidateDependency(dep, target); err != nil {
				return nil, fmt.Errorf("%w: %v", plugin.ErrMismatch, err)
			}
			identity := target.Package.Descriptor.Identity
			edge := packagekit.Dependency{Contract: plugin.ContractRef{Name: plugin.AdapterContractName, Version: dep.APIVersion}, Operation: "share", Provider: identity.ID}
			duplicate := false
			for _, r := range pm.Requires {
				// A foreign-target declaration cannot replace this target
				// obligation from adapter metadata.
				if (r.OS != "" && r.OS != goos) || (r.Arch != "" && r.Arch != arch) {
					continue
				}
				if r.Contract == edge.Contract && r.Operation == edge.Operation && ((r.Identity != nil && *r.Identity == identity) || r.Provider == edge.Provider) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				pm.Requires = append(pm.Requires, edge)
			}
		}
	}
	return result, nil
}

// HasDependencyDeclarations reports whether adapter planning is needed. Invalid
// metadata also requires planning so a decoder error cannot bypass preflight.
func HasDependencyDeclarations(pm packagekit.Manifest) bool {
	raw, ok := pm.Metadata[MetadataKey]
	if !ok {
		return false
	}
	values := map[string]string{}
	if err := metadataValues(raw, values); err != nil {
		return true
	}
	if values["share_dependencies"] != "" {
		return true
	}
	for _, platform := range []string{"darwin", "linux", "windows"} {
		if values["dependencies_"+platform] != "" {
			return true
		}
	}
	return false
}

// declaredDependencies joins adapter-owned share spaces to generic package
// requirements. Legacy declarations remain readable but cannot be mixed with
// the new form, so there is one authority for version and platform selection.
func declaredDependencies(pm packagekit.Manifest, values map[string]string, goos, arch string) (map[string]Dependency, error) {
	owner := pm.Descriptor.Identity.ID
	selected := map[string]Dependency{}
	modern := values["share_dependencies"] != ""
	for _, platform := range []string{"darwin", "linux", "windows"} {
		raw := values["dependencies_"+platform]
		if modern && raw != "" {
			return nil, fmt.Errorf("adapter %s mixes share_dependencies and legacy dependencies", owner)
		}
		deps, err := parseDependencies(raw, owner)
		if err != nil {
			return nil, err
		}
		if platform == goos {
			selected = deps
		}
	}
	seen := map[string]bool{}
	type obligation struct {
		dep      Dependency
		os, arch string
	}
	var obligations []obligation
	for _, entry := range splitList(values["share_dependencies"]) {
		space, provider, ok := strings.Cut(entry, ":")
		if !ok || !validName.MatchString(space) || !validName.MatchString(provider) || reservedNames[provider] || provider == owner || seen[entry] {
			return nil, fmt.Errorf("adapter %s has invalid share dependency %q; expected space:provider", owner, entry)
		}
		seen[entry] = true
		found := false
		for _, r := range pm.Requires {
			id := r.Provider
			if r.Identity != nil {
				id = r.Identity.ID
			}
			if id != provider || r.Contract.Name != plugin.AdapterContractName || r.Operation != "share" {
				continue
			}
			if r.Contract.Version != APIVersion {
				return nil, fmt.Errorf("adapter %s share dependency %s uses unsupported API %s", owner, entry, r.Contract.Version)
			}
			found = true
			dep := Dependency{Space: space, Adapter: provider, APIVersion: r.Contract.Version}
			for _, prior := range obligations {
				if prior.dep.Space == space && prior.dep != dep &&
					(prior.os == "" || r.OS == "" || prior.os == r.OS) &&
					(prior.arch == "" || r.Arch == "" || prior.arch == r.Arch) {
					return nil, fmt.Errorf("adapter %s has overlapping share dependencies for %s", owner, space)
				}
			}
			obligations = append(obligations, obligation{dep, r.OS, r.Arch})
			if (r.OS != "" && r.OS != goos) || (r.Arch != "" && r.Arch != arch) {
				continue
			}
			if prior, ok := selected[space]; ok && prior != dep {
				return nil, fmt.Errorf("adapter %s has ambiguous share dependency for %s on %s/%s", owner, space, goos, arch)
			}
			selected[space] = dep
		}
		if !found {
			return nil, fmt.Errorf("adapter %s share dependency %s has no named ext.adapter share requirement", owner, entry)
		}
	}
	return selected, nil
}
