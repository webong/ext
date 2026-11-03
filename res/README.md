# Shared resources

`res/` is a container for reusable modules. Each subdirectory is its own Go
module, except `web`, which is a container too (its Go module is `web/host`), and
nothing lives at this level: no Go package and no repository-wide
policy. `web/host` depends only on the standard library and wazero; `system` depends only on
`golang.org/x/sys`.

| Module | Import path | Contents |
| --- | --- | --- |
| [`web`](web/README.md) | `github.com/webong/ext/res/web/host` | Portable web contracts and workflows: browser cookies, extensions, userscripts and sessions, the web engine, and protocol-neutral WebAssembly execution. A container: the Go module is `web/host`, beside the iOS (`web/ios`), Android (`web/android`) and WebView2 (`web/windows`) modules. The browser-extension JavaScript library is [`pkg/ext-web`](../pkg/ext-web/README.md) |
| [`system`](system/README.md) | `github.com/webong/ext/res/system` | Local graphical and textual shell substrate: surfaces, capture, media, volumes, clipboard, credential integration, network-state observation, process supervision, and system observation. Depends only on `golang.org/x/sys` |
| [`int`](int/README.md) | `github.com/webong/ext/res/int` | Portable model, inference, perception, and real-time intelligence contracts and mechanisms |

All resource modules are listed in the root `go.work`, so the repository still builds as one
workspace while each module stays independently consumable.

Resources expose contracts and workflows that CTX, adapters, and other hosts can
reuse. They do not import CTX internals, product adapters, commands or examples;
`src/ctx/internal/arch` enforces that dependency direction across every reusable tree.

- `web/host/contract` owns browser request, response, cookie, policy, and session
  types and their validation.
- `web/host/manage/guest` serves portable browser workflows through adapter-provided
  backends. Native storage, discovery, and tools belong under `adapters/`.
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
