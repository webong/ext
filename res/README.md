# Shared resources

`res/` is a container for reusable modules. Each subdirectory is its own Go
module, and nothing lives at this level: no Go package, no repository-wide
policy. Both modules depend only on the standard library.

| Module | Import path | Contents |
| --- | --- | --- |
| [`browser`](browser/README.md) | `github.com/webong/ctx/res/browser` | Portable browser contracts, workflows, extension and userscript handling, cookie parsing |
| [`credential`](credential/README.md) | `github.com/webong/ctx/res/credential` | Portable credential serving (`adapterkit`) and CTX credential client |

Both are listed in the root `go.work`, so the repository still builds as one
workspace while each module stays independently consumable.

Resources expose contracts and workflows that CTX, adapters, and other hosts can
reuse. They do not import CTX internals, product adapters, commands or examples;
`internal/arch` enforces that dependency direction across every reusable tree.

- `browser/contract` owns browser request, response, cookie, policy, and session
  types and their validation.
- `browser/guest` serves portable browser workflows through adapter-provided
  backends. Native storage, discovery, and tools belong under `adapters/`.
- `browser` parses and combines cookies. `Get` and `Normalize` require an
  injected `browser.Backend` for source operations. `InlineOnly`, parsers, and
  output helpers work without a backend.
- `credential/adapterkit` serves credential operations against a supplied
  `Store`. The adapter executable parses its process envelope and passes an
  `Invocation` to `Run`.
- `credential/client` is a client for CTX's credential process protocol. It has
  no dependency on native store implementations; using it requires a CTX host.

CTX's browser backend lives in `internal/app/browserhost`. It owns installed
adapter discovery, configuration paths, capability checks, trust, and process
dispatch. Other hosts implement `browser.Backend` with their own selection and
authorization policy. No resource chooses a browser implementation by name.

For existing Go callers, replace implicit CTX discovery with an explicit
`Options.Backend` or `NormalizeOptions.Backend`; configure `AdapterHome` on the
CTX host provider rather than on the resource options. The CLI supplies its
provider automatically, so command-line behavior is unchanged.

These packages currently share the repository's Go module. Their dependency
direction already permits a module split, so each can gain its own `go.mod`,
version, and release tag without depending back on CTX root.
