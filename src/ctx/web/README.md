# src/ctx/web

`github.com/webong/ext/src/ctx/web` is the Go module of [`src/ctx/web`](docs/OVERVIEW.md): the web
code that runs on the host, outside any browser. Any ext product or other program can use
it. The module does not depend on the plugin engine, an adapter, a browser engine, or any
CTX application code. Releases are tagged `src/ctx/web/vX.Y.Z`; the module path
`github.com/webong/ext/src/ctx/web` that came before it is never tagged again.

Packages are grouped by role. The group directories (`manage`, `artifact`, `engine`) hold
packages, not Go files of their own.

| Package | Contents |
| --- | --- |
| `contract` | Request, response, cookie, policy and session types with their validation; native WebView script capability contracts; `session.endpoint` result validation and redaction (`EndpointResult`). Imports nothing else in the module |
| `manage/cookie` | Parses, queries and combines cookies. `Get` and `Normalize` use an injected `Backend`; `ListForSite`, `Select`, `ExportSelected`, `Import` and `Copy` use caller-selected browser backends and verify the selected cookie before transferring its value. ctx imports it as `browser` |
| `manage/sqlite` | Read-only SQLite reader for browser cookie stores (standard library only, no sqlite3 program) and the sqlite3 helper used by cookie import |
| `manage/guest` | Portable browser workflows served through adapter-provided backends |
| `manage/discovery` | Finding the native executables an adapter declares, without launching them |
| `artifact/extension` | Extension artifact discovery, packaging, install targets, and the progress line an adapter writes while an install runs |
| `artifact/userscript` | Userscript metadata, storage and replay, with language-neutral fixtures in `testdata/userscript` |
| `artifact/bookmarklet` | Bookmarklet generation, with language-neutral fixtures in `testdata/bookmarklet` |
| `engine/bundle` | The web engine: serves a web bundle from loopback under a content security policy and runs it in a browser the host opens, reporting console output and the exit code |
| `engine/wasm` | Runtime-neutral WebAssembly host contract: inspection results, WASI command options, byte-stream and reactor interfaces; also `reactor.js`, the shared page-side reactor glue embedded in the iOS and Android WebView hosts. The Go implementation is `pkg/ext-go/wasm/runtime` |

[The WebView example](../../../examples/web/go/plugin-webview) shows the engine with a plugin, and `internal/portcorpus`
writes the corpus that `scripts/web-differential.sh` uses to compare the Swift and Java
modules with Go.
Browser policy file and native tool reads are owned by the browser adapters.
Chromium-family Go adapters share an optional helper in
`xallet.app/tab/chromium/engine/policy`; Firefox and Safari own their reads. The
wire cases are in `tests/fixtures/web/policy` for adapters in any language.

## Layering

| Group | May import |
| --- | --- |
| `contract` | nothing else in the module |
| `artifact` | `contract` |
| `engine` | nothing else in the module |
| `manage` | `contract`, `artifact` |

`src/ctx/internal/arch` enforces these rules and the module path.

## Fixtures

The fixtures the Go tests read are in [`../testdata`](../../../tests/fixtures/web), beside this module
rather than inside it, because the Swift and Java modules run the same files. A copy of
this module without the repository around it has no fixtures, so the tests that read them
find them through `internal/fixtures`, which fails in a checkout when they are missing and
skips outside one.

Native storage, discovery and tooling belong in the owning adapter, outside ext
(see [adapter repositories](../../../docs/adapter-repositories.md)). No package here
chooses a browser implementation by name — hosts supply selection and authorization
policy.
