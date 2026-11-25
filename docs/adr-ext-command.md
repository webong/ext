# One public `ext` command with product packages

Status: implemented in the source, release builder and installers. No release has
been published from this change yet.

`ext` is the only public executable. CTX, CTN and CTO remain independent Go
modules under `src/`, with public `Run` functions for other Go projects. Their
Go module paths match their directories (`github.com/webong/ext/src/ctx`,
`github.com/webong/ext/src/ctn`, and `github.com/webong/ext/src/cto`). Their
private `plugin/` entrypoints are built as installed Ext packages. A product
does not import another product, and `cmd/ext` does not link them into its binary.

## Dispatch

`ext ctx`, `ext ctn`, `ext cto` and `ext sandbox` resolve through the same package
registry as any other command. Each package declares its commands in the
`ext.command/v1` manifest metadata, serves `ext.command.run` and is verified
before launch. `ext commands` and `ext which` show the package source and version.
Product package entrypoints are private artifacts, not user-facing binaries.

The first-party package directories ship beside `bin/ext` under
`bin/ext-packages/`. They are part of the same release trust boundary as `ext`;
external packages installed under `EXT_HOME/packages` still need explicit
`ext package trust` approval tied to their content digest. A package from a
different source cannot silently replace a first-party command with the same
name.

Commands that need normal standard streams declare `transport: "socket"`.
Ext starts their guest with inherited stdin and connected stdout/stderr, and
carries plugin frames on a local loopback connection authenticated by a random
per-launch token. This preserves CTX's piped input and CTN's command output.
The ordinary JSON-line stdio package transport remains available to other
packages. Descriptor flags in the trusted declaration permit explicit numeric
file descriptors to reach a product that needs them, such as CTO's launch and
status descriptors. Ext's host dispatch lives in `cmd/ext/internal/pkgcmd`;
each product owns its private guest entrypoint under `src/<product>/plugin/`.

The plugin engine has reverse calls and a route registry for guest-to-guest
communication. The command socket path currently opens one short-lived guest
per CLI invocation and has no route policy wired to it. Product-to-product
calls should use declared plugin capabilities and routes through Ext when that
policy is added; one product must not import or launch another by name.

## Releases

`ext-vX.Y.Z` builds the public `ext` binary and the three verified package
artifacts for each supported platform. The archive has one public executable.
CTX, CTN and CTO keep their own Go module APIs and version labels inside their
package manifests. Publish module path tags for the product libraries and their
new dependencies before external `go get`; those tags are independent of the
`ext-v*` binary tag. New standalone `ctx-v*` and `ctn-v*` binary releases stop.

## Packages without commands

`ext package` installs, upgrades, trusts, lists and removes valid plugin packages
whether or not they expose `ext.command/v1`. Trust approves the verified package
digest for this host; it does not create commands or grant authority in adapter
or other consumer scopes. Invalid command metadata is reported separately and
never registered, while the package can still be managed for its other contracts.
The shared store is `EXT_HOME/packages`, with scoped trust in its hidden
`.package-trust.json` ledger. Old command and adapter stores and their trust
files are no longer imported. Reinstall reviewed packages into the current store
and approve them explicitly. Adapter and command approvals remain separate.


Native library preparation is exposed directly as `ext bundle fetch` and
`ext bundle bind`, including through `go tool ext bundle ...`. There is no
intermediate `lib` subcommand or compatibility alias for the old top-level name.
See [the package command model](adr-cli-package-model.md).
