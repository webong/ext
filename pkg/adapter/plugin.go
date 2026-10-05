package adapter

import "github.com/webong/ctx/pkg/plugin"

// PluginContractName identifies CTX's argv/stream binding. Its version is the
// adapter API, independently of the shared plugin envelope's API version.
const PluginContractName = "ctx.adapter"

// PluginDescriptor translates a reviewed adapter package into the common
// contract. The caller supplies a content revision and declared operations.
// CLI adapters retain their argv, environment, exit status, and streams; this
// descriptor does not claim that they serve JSON-line plugin handshakes.
func PluginDescriptor(identity plugin.Identity, operations []string) plugin.Descriptor {
	c := plugin.Contract{ContractRef: plugin.ContractRef{Name: PluginContractName, Version: APIVersion}}
	seen := map[string]bool{}
	for _, name := range operations {
		if seen[name] {
			continue
		}
		seen[name] = true
		c.Operations = append(c.Operations, plugin.Operation{Name: name})
	}
	return plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: identity, Contracts: []plugin.Contract{c}}
}
