# src/ctx/web

`src/ctx/web` owns the Ext-facing web contracts, fixtures and guides. It holds no Go
code at its top. The native Apple, Android and Windows libraries are the `web` libraries of
[`pkg/ext-darwin`](../../../../pkg/ext-darwin/web/README.md), [`pkg/ext-android`](../../../../pkg/ext-android/web/README.md) and [`pkg/ext-windows`](../../../../pkg/ext-windows/web/README.md). The published Go host module
remains here during migration; `pkg/ext-go/wasm/runtime` implements
the WebAssembly host contract with wazero and `pkg/ext-go/wasm` adds the Ext plugin
binding.

| Directory | What it is | Built and tested with |
| --- | --- | --- |
| [`host`](../README.md) | The Go module `github.com/webong/ext/src/ctx/web`: web code that runs on the host, outside any browser | `go test ./...` in `host` |
| [`pkg/ext-darwin/web`](../../../../pkg/ext-darwin/web/README.md) | The SwiftPM packages `ExtWeb` and `ExtWebKit` | `scripts/web-ios.sh` |
| [`pkg/ext-android/web`](../../../../pkg/ext-android/web/README.md) | The Java package `io.github.webong.ext.web` | `scripts/web-android.sh` |
| [`pkg/ext-windows/web`](../../../../pkg/ext-windows/web/README.md) | The .NET library `Ext.Web.Windows` | `dotnet build` and its console test project |
| [`docs`](..) | Guides for the mobile modules | |
| [`testdata`](../../../../tests/fixtures/web) | Language-neutral fixtures that the Go, Swift and Java modules all run | |

The JavaScript/TypeScript library for code that runs inside a browser extension is not
in this container: it is [`pkg/ext-web`](../../../../pkg/ext-web/README.md), named for its
platform like `pkg/ext-darwin` and `pkg/ext-android`.

## What it provides

| It provides | Where | What it is for |
| --- | --- | --- |
| **A browser-management kit** | `host/contract`, `host/manage`, `host/artifact/extension` | The protocol that hosts and browser adapters agree on, the workflows served through adapter-provided backends, and the mechanisms every browser adapter needs: artifact inspection and packaging, executable discovery, policy files, and reading cookie stores |
| **Web artifacts on every runtime** | `host/artifact`, the platform packages' `web` libraries, and `pkg/ext-web` | The same rules for each place web code runs: the host (Go), inside a native app's WebView, and inside a browser extension (`pkg/ext-web`) |
| **A way to run web content** | `host/engine` | The web engine serves a bundle to a browser the host opens; `engine/wasm` defines a runtime-neutral WebAssembly host contract implemented with wazero in `pkg/ext-go/wasm/runtime`. Neither manages a browser |

Native storage, discovery and tooling belong in the owning adapter, outside ext
(see [adapter repositories](../../../../docs/adapter-repositories.md)). Nothing here
chooses a browser implementation by name — hosts supply selection and authorization
policy.

## Platform modules

`pkg/ext-darwin/web` and `pkg/ext-android/web` are the cookie, userscript and bookmarklet rules written out for their
platform, so a phone app needs no Go runtime and no native library for them. `windows`
hosts prepared userscripts in WebView2. None of them is a Go package, and none is part
of the `host` module zip or its version: apps take the mobile modules through
[`pkg/ext-darwin`](../../../../pkg/ext-darwin/README.md) and
[`pkg/ext-android`](../../../../pkg/ext-android/README.md), which ship them beside their plugin
code and release under their own tags.

Go, Swift and Java run the fixtures in `testdata`, and `scripts/web-differential.sh`
compares the Swift and Java modules with the Go one on a corpus that
`host/internal/portcorpus` writes. A change to these rules lands in all three, with a
fixture. `pkg/ext-web` has its own tests and does not share those fixtures yet.

The WebView2 target accepts reviewed source already prepared by the portable
userscript workflow. It does not choose a browser profile or authorize
extension changes; an application owns those decisions and the CoreWebView2
lifetime.

`WorkspaceScripts.prepare` in the mobile modules validates a complete set of
reviewed userscript records, rejects duplicate IDs and target mismatches, and
generates replay source for enabled records. Native workspaces also live here:
[`ExtWebKit`](../../../../pkg/ext-darwin/web/WORKSPACE.md) and
[`Android WebView`](../../../../pkg/ext-android/web/WORKSPACE.md), shipped by the platform SDKs.
Separate targets/source sets keep the portable rules free of native webview imports.

`userscript.InspectCompatibility`, `Userscript.compatibility(of:)`, and
`Userscript.compatibility` inspect Greasemonkey-family metadata before installation. They report
unsupported grants, external resources, legacy include/exclude rules, frame scope and execution
timing as stable blocker codes; unsupported requirements are never silently discarded.

Native workspaces expose separate current-page and future-document script capabilities. Current-page
evaluation is tied to a navigation generation so an operation cannot report success after the page
has changed underneath it.

## Guides

- Mobile modules, in [`docs`](..):
  [cookie workflows](COOKIE_WORKFLOWS.md) for portable import/export and native workspace cookie access,
  [cookie queries](COOKIE_QUERY.md) for host-authorized source ordering, merge/first modes, fallback,
  provenance and cancellation,
  [extension artifacts](EXTENSION_ARTIFACTS.md) for inspection, unsigned ZIP packaging, revision-checked
  staging and artifact helpers,
  [the browser-management protocol](MANAGEMENT.md) for Swift/Java envelopes, validation, endpoint safety
  and backend dispatch,
  [workspace management and persistence](WORKSPACE_MANAGEMENT.md) for native management routing and
  Go-compatible userscript storage, and
  [local web bundle runners](BUNDLE_RUNNER.md) for native HTML/module execution, CSP, console events,
  host messaging and lifecycle handling.
- Browser extension library: [`pkg/ext-web/README.md`](../../../../pkg/ext-web/README.md),
  [`PLAN.md`](../../../../pkg/ext-web/PLAN.md) and [`COMPATIBILITY.md`](../../../../pkg/ext-web/COMPATIBILITY.md).
- WebAssembly: [`host/engine/wasm/README.md`](../../../../pkg/ext-go/core/wasm/README.md).
- Repository docs that use this library:
  [browser profile and page management](../../../../docs/browser-management.md),
  [browser cookies and automation input](../../../../docs/browser-cookies.md),
  [the web engine](../../../../docs/web-engine.md),
  [extension distribution](../../../../docs/extension-distribution.md),
  [the adapter API](../../../../docs/adapter-api.md) and
  [why the module is laid out this way](../../../../docs/adr-web-layout.md).
