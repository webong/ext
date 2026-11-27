# Ext resources

`res/` owns versioned Ext resource contracts and their guest entrypoints.
Reusable native OS, web and intelligence implementations live in
[`lib/system`](../lib/system/README.md). The existing Go modules in `res/` keep
their published import paths during migration; they still contain Go mechanisms
that have not yet been moved behind Ext guests. Nothing lives at the `res/` root.

| Module | Import path | Contents |
| --- | --- | --- |
| [`web`](web/README.md) | `github.com/webong/ext/res/web/host` | Existing Go web contracts and workflows, Ext-facing fixtures and guides; native web libraries are in `lib/system/web` |
| [`system`](system/README.md) | `github.com/webong/ext/res/system` | Existing Go system APIs, contracts and fixtures; native platform libraries are in `lib/system/act/capture` and `lib/system/service` |
| [`int`](int/README.md) | `github.com/webong/ext/res/int` | Existing Go intelligence contracts and Ext model guests; native model libraries are in `lib/system/int` |

All resource modules are listed in the root `go.work`, so the repository still builds as one
workspace while each module stays independently consumable.

Resources expose contracts and workflows that CTX, adapters, and other hosts can
reuse. They do not import CTX internals, product adapters, commands or examples;
`src/ctx/internal/arch` enforces that dependency direction across every reusable tree.

- `web/host/contract` owns browser request, response, cookie, policy, and session
  types and their validation.
- `web/host/manage/guest` serves portable browser workflows through adapter-provided
  backends. Native storage, discovery, and tools belong in the owning adapter,
  outside ext (see [adapter repositories](../docs/adapter-repositories.md)).
- `web/host/manage/cookie` (imported by ctx as `browser`) parses and combines cookies. `Get`
  and `Normalize` require an injected `browser.Backend` for source operations.
  `InlineOnly`, parsers, and output helpers work without a backend.
- `system/credential/adapterkit` serves credential operations against a supplied
  `Store`. The adapter executable parses its process envelope and passes an
  `Invocation` to `Run`.
- `system/credential/client` is a client for CTX's credential process protocol. It has
  no dependency on native store implementations; using it requires a CTX host.

CTX's browser backend lives in `internal/app/browserhost`. It owns installed
adapter discovery, configuration paths, capability checks, trust, and process
dispatch. Other hosts implement `browser.Backend` with their own selection and
authorization policy. No resource chooses a browser implementation by name.

For existing Go callers, replace implicit CTX discovery with an explicit
`Options.Backend` or `NormalizeOptions.Backend`; configure `AdapterHome` on the
CTX host provider rather than on the resource options. The CLI supplies its
provider automatically, so command-line behavior is unchanged.

Each resource is already an independently versioned module. Packages nested
under `system/`, including clipboard, credential and network integration, share the
`res/system` module and release tag without depending back on the CTX CLI.
