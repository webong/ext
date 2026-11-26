# Core ML provider

This Swift guest implements `model.tensor/v1` as the `coreml` provider. It
uses the published `ExtPluginGuest` SDK for the plugin protocol and loads
verified models through the published macOS `ExtML` library. The SwiftPM
manifest pins an `ext-macos` distribution commit and has no workspace path, so
this directory can be copied to another repository and built there. CTN
chooses the installed provider and model.

From this directory, build and seal the private guest on macOS. `package.sh`
uses generic `ext package pack` with this adapter’s `descriptor.json` and writes a platform ZIP and `index.json`
that can be served by an Ext package source:

```sh
./package.sh
ext install ./out/coreml.darwin-arm64
ext package list
ext package trust coreml
```

On Intel macOS the package directory ends in `darwin-amd64`. A remote package
source can serve `out/index.json` and its ZIP on the same HTTPS origin; an
installer uses `ext source add`, `ext install --source`, then
`ext package trust coreml`.

The package's `ext.json` declares `model.tensor/v1`. Ext installs it into the
shared package registry and reports that capability without a CTN-specific
installation route. CTN opens it through Ext's shared
package store when a caller selects `coreml`, and the guest runs the model with
the published `ExtML` library. Update the pinned commit when adopting a newer
`ext-macos` release; the distribution tag names a commit, while SwiftPM needs
that commit's revision in the manifest.
