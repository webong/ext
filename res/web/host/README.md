# res/web/host

`github.com/webong/ext/res/web/host` is the Go module of [`res/web`](../README.md): the web
code that runs on the host, outside any browser. Any ext product or other program can use
it. The module does not depend on the plugin engine, an adapter, a browser engine, or any
CTX application code. Releases are tagged `res/web/host/vX.Y.Z`; the module path
`github.com/webong/ext/res/web` that came before it is never tagged again.

Packages are grouped by role. The group directories (`manage`, `artifact`, `engine`) hold
packages, not Go files of their own.

| Package | Contents |
| --- | --- |
| `contract` | Request, response, cookie, policy and session types with their validation; native WebView script capability contracts; `session.endpoint` result validation and redaction (`EndpointResult`). Imports nothing else in the module |
| `manage/cookie` | Parses, queries and combines cookies. `Get` and `Normalize` require an injected `Backend` for source operations; `InlineOnly`, the parsers and the output helpers work without one. ctx imports it as `browser` |
| `manage/sqlite` | Read-only SQLite reader for browser cookie stores (standard library only, no sqlite3 program) and the sqlite3 helper used by cookie import |
| `manage/guest` | Portable browser workflows served through adapter-provided backends |
| `manage/discovery` | Finding the native executables an adapter declares, without launching them |
| `manage/policy` | Exports the browser policies found at the locations an adapter declares (policy files, policy directories and a registry key), and nowhere else |
| `artifact/extension` | Extension artifact discovery, packaging, install targets, and the progress line an adapter writes while an install runs |
| `artifact/userscript` | Userscript metadata, storage and replay, with language-neutral fixtures in `testdata/userscript` |
| `artifact/bookmarklet` | Bookmarklet generation, with language-neutral fixtures in `testdata/bookmarklet` |
| `engine/bundle` | The web engine: serves a web bundle from loopback under a content security policy and runs it in a browser the host opens, reporting console output and the exit code |
| `engine/wasm` | Protocol-neutral Go APIs for module inspection, bounded WASI command execution, persistent byte streams and raw reactor hosting through wazero; also `reactor.js`, the shared page-side reactor glue embedded in the iOS and Android WebView hosts |

`examples/plugin-webview` shows the engine with a plugin, and `internal/portcorpus`
writes the corpus that `scripts/web-differential.sh` uses to compare the Swift and Java
modules with Go.

## Layering

| Group | May import |
| --- | --- |
| `contract` | nothing else in the module |
| `artifact` | `contract` |
| `engine` | nothing else in the module |
| `manage` | `contract`, `artifact` |

`src/ctx/internal/arch` enforces these rules and the module path.

## Fixtures

The fixtures the Go tests read are in [`../testdata`](../testdata), beside this module
rather than inside it, because the Swift and Java modules run the same files. A copy of
this module without the repository around it has no fixtures, so the tests that read them
find them through `internal/fixtures`, which fails in a checkout when they are missing and
skips outside one.

Native storage, discovery and tooling belong under `adapters/<name>/`. No package here
chooses a browser implementation by name — hosts supply selection and authorization
policy.
