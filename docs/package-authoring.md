# Authoring Ext packages

Package authoring is independent of the consuming program and the contracts it
accepts. Command plugins, resource guests, providers and tool adapters use the
same `ext.io/v1` manifest and commands. Ext does not choose a toolchain or
invoke an operation named `doctor` based on a package's identity or capability.

## Directory packing

The CLI defaults to `ext.json`; this is a host convention, not a schema rule.
Use `--manifest metadata/package.json` with `pack`, `build` or `test` to choose
a different relative filename. The archive retains that path. An installing
host must select the same layout.

Prepare the manifest and the artifacts it declares, then run:

```sh
ext package test ./ready --os linux --arch amd64
ext package pack ./ready --os linux --arch amd64 --output ./provider-linux-amd64.zip
ext package index ./index.json ./provider-linux-amd64.zip
```

`pack` copies only declared files and trees into a private staging directory,
checks existing hashes, fills missing hashes and revisions, and writes a new
ZIP with `ext.json`. Metadata, requirements, schemas and capabilities remain
part of the manifest. Unlisted files are not shipped. Symbolic links, unsafe
paths, overlapping artifacts and oversized packages are rejected. Existing
output is never overwritten and the source directory is not modified.

Artifact `os` and `arch` fields select targets. Empty fields mean portable;
Ext does not infer platforms from filenames or tool-adapter metadata. Entries
and assets referring to excluded artifacts are excluded too. Declare concrete
targets on native executables. A single entrypoint's artifact hash supplies a
missing revision, matching guests that report their executable hash. For other
packages a missing revision is derived from the manifest with sealed artifacts;
an explicitly supplied revision is preserved.

## Builds in any language

Supply the build command explicitly after `--`:

```sh
ext package build ./source --os linux --arch amd64 --output ./provider.zip -- go build -o guest .
ext package build ./source --os linux --arch amd64 --output ./native.zip -- make guest
```

Ext runs the command directly in an isolated copy of the source, then packs the
outputs declared by that copy's `ext.json`. The command must produce those
artifacts. It may use `EXT_PACKAGE_OS` and `EXT_PACKAGE_ARCH`; Go also receives
`GOOS`, `GOARCH`, and `GOWORK=off`. The source must be independently buildable with
published dependencies. Ext does not infer `go_entry`, execute an implicit shell,
or choose a cgo policy. Build commands are author-authorized code execution;
isolation here protects the source copy, not the host OS. Use `pack` for outputs
built by an external toolchain.

## Checks and diagnostics

`ext package test DIRECTORY` statically checks a template and its declared
artifacts for the selected target, using the same preparation as packing. It
neither executes a guest nor proves behavioral contract conformance. Run a
contract's own acceptance suite separately.

`ext package doctor ID` verifies an installed package's integrity, trust and
artifact target compatibility. It reports declared contract count and fails
when trust or target compatibility is missing. It does not execute plugin code,
guess a diagnostics operation, or promise that external runtime dependencies
are installed. Consumers own behavioral checks and authorization for invocation.

## Single executable

```sh
ext package pack --descriptor descriptor.json --protocol example.api/v1 \
  --binary ./guest --output ./sealed --os linux --arch amd64
```

This form writes a sealed directory from a guest descriptor. Directory packing
can turn it into a ZIP. Both forms reuse shared authoring code and grant no trust.

## Manifest and runtime versions

```json
{
  "apiVersion": "ext.io/v1",
  "kind": "package",
  "identity": {"id": "example/provider", "revision": "r1"},
  "contracts": [{"name": "example.api", "version": "v1", "operations": [{"name": "read"}]}]
}
```

This is a valid declaration for a provider contributed by another host. A local
executable package also declares artifacts and entrypoints. Source templates can
leave hashes and revisions for packing to fill; sealed local packages must pass
integrity validation. `requires`, metadata, assets and configuration schemas keep
their existing roles. Installation never grants trust.

Runtime descriptors are derived from identity, contracts and reverse contracts.
They use `ext.io.pc/v1` and the handshake operation `session.hello`. They do not
carry artifact paths or package-installation policy. Previous manifest and runtime
versions are rejected: rebuild guests and reseal packages together. Archive hashes
and trust approvals change when package bytes change.
