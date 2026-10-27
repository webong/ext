# res/web

`github.com/webong/ext/res/web` holds ext's portable web contracts and workflows:
browsers (cookies, extensions, userscripts, sessions) today, and the web engine
that runs web content through them. It also owns bounded WebAssembly inspection,
WASI command execution, persistent byte streams and raw reactor hosting through
wazero. Any ext product or other program can use it. The Go module does not
depend on the plugin engine, an adapter, a browser engine, or any CTX application
code. `pkg/ext-go/wasm` adds the ext plugin binding.

| Package | Contents |
| --- | --- |
| `contract` | Request, response, cookie, policy and session types with their validation; native WebView script capability contracts; cookie fixtures in `testdata/cookie`; `session.endpoint` result validation and redaction (`EndpointResult`) |
| `guest` | Portable browser workflows served through adapter-provided backends |
| `extension` | Extension artifact discovery, packaging and install targets |
| `bundle` | The web engine: serves a web bundle from loopback under a content security policy and runs it in a browser the host opens, reporting console output and the exit code |
| `wasm` | Protocol-neutral Go APIs for module inspection, bounded WASI command execution, persistent byte streams and raw reactor hosting through wazero; also `reactor.js`, the shared page-side reactor glue embedded in the iOS and Android WebView hosts |
| `sqlite` | Read-only SQLite reader for browser cookie stores (standard library only, no sqlite3 program) and the sqlite3 helper used by cookie import |
| `userscript` | Userscript metadata, storage and replay, with language-neutral fixtures in `testdata/userscript` |
| `bookmarklet` | Bookmarklet generation, with language-neutral fixtures in `testdata/bookmarklet` |
| `ios`, `android` | Not Go packages: the Swift and Java modules of the userscript, cookie and bookmarklet rules (see Platform modules) |
| `windows` | The .NET WebView2 workspace for prepared userscripts and profile-extension mechanics |

`browser` itself parses and combines cookies. `Get` and `Normalize` require an
injected `browser.Backend` for source operations; `InlineOnly`, the parsers and
the output helpers work without one.

Native storage, discovery and tooling belong under `adapters/<name>/`. No
package here chooses a browser implementation by name — hosts supply selection
and authorization policy.

## Platform modules

`res/web` is a Go module, and also the iOS module and the Android module of the cookie, userscript and
bookmarklet rules. Each is the same rules written out for its platform, so a phone app needs no Go runtime and
no native library for them.

| Directory | Module | Built and tested with |
| --- | --- | --- |
| this directory | the Go module `github.com/webong/ext/res/web` | `go test ./...` |
| [`ios`](ios/README.md) | the SwiftPM package `ExtWeb` (macOS 12, iOS 13) | `scripts/web-ios.sh` (Swift only) |
| [`android`](android/README.md) | the Java package `io.github.webong.ext.web` | `scripts/web-android.sh` (a JDK only) |
| [`windows`](windows/README.md) | the .NET library `Ext.Web.Windows` | `dotnet build` and its console test project |

All three run the fixtures in `testdata`, and `scripts/web-differential.sh` compares the Swift and Java
modules with the Go one on a corpus that `internal/portcorpus` writes. A change to these rules lands in all
three, with a fixture. The mobile modules are not Go and are not versioned as part of the Go module: apps take
them through [`pkg/ext-ios`](../../pkg/ext-ios/README.md) and [`pkg/ext-android`](../../pkg/ext-android/README.md),
which ship them beside their plugin code and release under their own tags.

The WebView2 target accepts reviewed source already prepared by the portable
userscript workflow. It does not choose a browser profile or authorize
extension changes; an application owns those decisions and the CoreWebView2
lifetime.

See [browser profile and page management](../../docs/browser-management.md),
[browser cookies and automation input](../../docs/browser-cookies.md), and the
[adapter API](../../docs/adapter-api.md).

`WorkspaceScripts.prepare` in the mobile modules validates a complete set of
reviewed userscript records, rejects duplicate IDs and target mismatches, and
generates replay source for enabled records. Native workspaces also live here:
[`ios/Sources/ExtWebKit`](ios/WORKSPACE.md) and
[`android/src/android`](android/WORKSPACE.md), shipped by the platform SDKs.
Separate targets/source sets keep the portable rules free of native webview imports.

See [mobile cookie workflows](COOKIE_WORKFLOWS.md) for portable import/export and native workspace cookie access.

See [mobile browser-management protocol](MANAGEMENT.md) for Swift/Java envelopes, validation, endpoint safety and backend dispatch.

See [workspace management and persistence](WORKSPACE_MANAGEMENT.md) for native management routing and Go-compatible userscript storage.

`userscript.InspectCompatibility`, `Userscript.compatibility(of:)`, and
`Userscript.compatibility` inspect Greasemonkey-family metadata before installation. They report
unsupported grants, external resources, legacy include/exclude rules, frame scope and execution
timing as stable blocker codes; unsupported requirements are never silently discarded.

Native workspaces expose separate current-page and future-document script capabilities. Current-page
evaluation is tied to a navigation generation so an operation cannot report success after the page
has changed underneath it.

See [local web bundle runners](BUNDLE_RUNNER.md) for native HTML/module execution, CSP, console events, host messaging and lifecycle handling.

See [mobile cookie queries](COOKIE_QUERY.md) for host-authorized source ordering, merge/first modes, fallback, provenance and cancellation.

See [mobile extension artifacts](EXTENSION_ARTIFACTS.md) for inspection, unsigned ZIP packaging, revision-checked staging and artifact helpers.
