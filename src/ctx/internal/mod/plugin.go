package mod

import "github.com/webong/ext/pkg/plugin"

// PluginDescriptor gives CTX adapters the same immutable selection contract
// used by library consumers. The directory digest covers all adapter assets.
func (a *Adapter) PluginDescriptor() (plugin.Descriptor, error) {
	digest, err := a.Checksum()
	if err != nil {
		return plugin.Descriptor{}, err
	}
	d := plugin.AdapterDescriptor(plugin.Identity{ID: "ctx.adapter/" + a.Manifest.Name, Revision: "sha256:" + digest}, a.pluginOperations())
	// Legacy argv bindings keep their declared native API version.
	d.Contracts[0].Version = a.Manifest.APIVersion
	if err := d.Validate(); err != nil {
		return plugin.Descriptor{}, err
	}
	return d, nil
}

func (a *Adapter) pluginOperations() []string {
	ops := append([]string(nil), a.Manifest.Capabilities...)
	ops = append(ops, a.Manifest.ComputerCapabilities...)
	if a.IsRuntime("browser") && len(a.Manifest.BrowserManagement) > 0 {
		ops = append(ops, "manage")
	}
	return ops
}
