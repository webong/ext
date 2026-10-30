# Adapter ownership

Anything specific to a product, provider, browser, or native tool belongs in its
adapter package under `adapters/<name>/`. This includes native protocols,
installation and activation, executable and profile discovery conventions,
storage formats, signing tools, store URLs, settings paths, and diagnostics.

Ext libraries own generic adapter contracts, validation, package trust, and
invocation. CTX and CTN are products built on Ext and can run standalone; each
product owns its own selection and command dispatch. Adapters are ext adapters,
not owned by the interface invoking them. `ext` is the main installer and combined
entry point; CTX and CTN consume the declared capabilities available to them,
including in standalone mode. Current adapter management uses `ext ctx adapter`
or standalone `ctx adapter`; `ctn adapter ls` is read-only. See
[adapter management](docs/adr-adapter-management.md).

Shared libraries may
implement portable workflows and utilities, but must not choose native behavior
by adapter name or supply a browser-specific fallback. Adapters provide that
behavior through declared capabilities and backends.

Related adapters may reuse an engine from the adapter that owns the native
mechanism, such as `adapters/chromium/engine`. Each product adapter supplies its
own paths, identifiers, capabilities, and policy through configuration.

Apply this boundary when bringing code into Ext: move provider-specific behavior
into the owning adapter before wiring it into shared workflows.

A platform implementation of a portable public-library API is not an adapter
merely because it calls a native framework. Keep those implementations beside
their portable contract in the owning library. An adapter represents a
selectable external product, provider, browser, or native service and supplies
that selection through an adapter capability or backend. For example, Safari
and Chrome integration belongs in their adapters, while the WKWebView and
Android WebView implementations of the public `res/web` workspace API belong in
`res/web/ios` and `res/web/android`.

`pkg/ext-web-js` is a private staging copy for a library Cymonkey will own and
distribute. Its extension-embedded runtime messaging, in-browser management,
themes, and `chrome`/`browser` compatibility are not adapters. External
browser installation, profile discovery, signing, and management remain
adapter-owned in ext. See [the handoff](pkg/ext-web-js/HANDOFF.md).

# Public libraries

`pkg/` is the home for reusable libraries. Adapters, CTX, CTN, and other
ecosystem consumers build on them; `internal/` stays private to its owning
product and must not be imported by consumers.
`pkg/ext-web-js` temporarily holds the standalone JavaScript/TypeScript browser
extension peer and theme library while it is prepared for Cymonkey ownership.
It is not distributed from ext. Its independently buildable examples live inside
that package until transfer.

The repository root is only the workspace: it holds `go.work` and no Go module or
package. Binary releases use product tags, `ctx-vX.Y.Z` and `ctn-vX.Y.Z`; those are not Go
module versions. Bare `vX.Y.Z` tags are not used.
`src/` is a container like `res/`; the context manager is the module
`src/ctx` (`src/ctx/cmd/ctx`, with its private code in `src/ctx/internal`) and
the content manager is `src/ctn`. This repository is ext: it provides libraries
and binaries, and ctx and ctn are binaries built on those libraries. A product
never imports another product; shared code goes in `pkg/` or `res/`.
`examples/` is not a Go module: it holds only samples that need no Go build (a shell adapter and an Android
consumer app). Go examples live in the library they show, as an `examples/` directory inside that module:
`pkg/ext-go/examples` for plugins (the umbrella requires every backend), `res/system/examples` and
`res/web/examples` for those libraries. Nothing but `cmd/ext` may import the product modules `github.com/webong/ext/ctx` or
`github.com/webong/ext/ctn`, through their root packages (`ctx.Run`, `ctn.Run`; see [the ext command](docs/adr-ext-command.md)); their directories are `src/ctx` and `src/ctn`. The module paths deliberately omit
`src/`, so the products are not `go get`-resolvable from the repository until their
paths and directories match; they are built from source and released as binaries.

`res/` is a container for reusable modules rather than a package. Each
subdirectory is its own Go module, and nothing lives at that level: no Go package
and no repository-wide policy. Keep container directories free of shared logic
and keep repo-wide tests in `src/ctx/internal/arch`. `res/web` depends on wazero
for its protocol-neutral WebAssembly runtime and never on the plugin engine;
`res/system` depends on `golang.org/x/sys`.

`lib/` is a container for native libraries that several language bindings
share. Nothing lives at that level. `lib/plugin` is the portable C plugin
engine, built with CMake: it is not a Go module, has no `go.mod` and is not
listed in `go.work`. The language bindings (`pkg/ext-go`, `pkg/ext-rust`, `pkg/ext-zig`, `pkg/ext-ts`,
`pkg/ext-python`) and the mobile SDKs (`pkg/ext-ios`, `pkg/ext-android`) build on it.
Until it reaches parity, the pure-Go implementation in `pkg/ext-go/engine`
(and the pure-Go build of `pkg/ext-go/cgoengine`) is the behavioral baseline; see `docs/plugin-engine-parity.md`. The shared conformance
fixtures live in `lib/plugin/testdata`. A change to the plugin contract or the
package manifest lands in both implementations, and the conformance checks
compare the two.

Each reusable library under `pkg/` is its own Go module, not a package inside
one repository-wide module. The root `go.work` keeps the repository building as
a single workspace while every module stays independently buildable. Keep each
library's dependencies pointing inward so it never depends back on its consumers;
`src/ctx/internal/arch` enforces that direction.

`pkg/ext-go/` is a tree of modules, not one module, so an application takes
only the parts it needs: `engine` (standard library only), `cgoengine` (the C
engine binding, with a pure-Go build of the same API), `wasm` (the plugin binding
for `res/web/wasm`), `hashicorp` (go-plugin, grpc), `inference` (the provider
bridge), `model` (the tensor-provider bridge), `perception` (the content
understanding bridge), and `realtime` (the duplex-session bridge), plus `pkg/ext-go` itself
as a thin umbrella module that requires them at matching versions. Nested
modules are excluded
from the module that contains them, so an import path must belong to exactly one
module and never move between modules at a version a consumer may still resolve;
published paths are not reused by a different module.

Runtime backends that own a heavy runtime stay outside the engine module:
`pkg/ext-go/hashicorp` is a separate module for go-plugin/grpc, while
`pkg/ext-go/wasm` binds the protocol-neutral wazero runtime in `res/web/wasm`
to `engine.Backend`. A backend never depends on `pkg/ext-go/cgoengine`, so a
wazero or grpc user needs no C toolchain and the engine binding is not in their
module graph. Tests that need
a backend belong in that backend's module, and tests or commands that need both
a backend and the engine belong in the umbrella module, not in `engine`, so
the core stays free of every runtime. `pkg/ext-go/engine` uses the standard
library only. `src/ctx/internal/arch` enforces both rules.

When adding a library module, list it in `go.work`, give it a `go.mod`, and add
matching `require` and `replace` directives to `src/ctx/go.mod` only if ctx imports it
(adapters are loaded as ext packages, so `src/ctx/go.mod` never lists them). Do not add
`replace` directives to library modules: Go ignores them for consumers, so they
only misrepresent what a published module resolves. `GOWORK=off go build` in a
library module is expected to fail until its dependencies are published.

Test each module in workspace mode, and note that `go test ./...` only covers the
module in the current directory. Versioning follows [module versioning](docs/module-versioning.md):
one shared `MAJOR` across all modules, independent `MINOR` and `PATCH`, and tags
named for each module path.

CTN owns its content verification workflow and durable file catalog in
`src/ctn/internal/content`. They are private product code, not a reusable module.

The reusable libraries are:

- `res/web` for portable web contracts and workflows: browsers, WebAssembly inspection/execution through wazero (`res/web/wasm`), and the web engine that runs web content through browsers. It is also the iOS and Android module of the cookie, userscript and bookmarklet rules (`res/web/ios`, `res/web/android`), which `pkg/ext-ios` and `pkg/ext-android` ship, and owns the application-hosted WebView2 implementation in `res/web/windows`.
- `lib/plugin` for the portable C plugin engine shared by the language bindings under `pkg/` (`ext-go`, `ext-rust`, `ext-zig`, `ext-ts`, `ext-python`) and the mobile SDKs.
- `res/system` for the local operating-system substrate used to extend graphical and textual shells: host, shell, filesystem and volume mechanics, clipboard, credential integration, local interface/address/route/resolver observation, web view and process discovery, native surfaces, accessibility, permission state, bounded capture and media playback/capture, and atomic native input. It owns mechanisms, not authorization, remote control, routing services or consumer policy. Its subpackages include `res/system/clipboard`, `res/system/credential`, `res/system/graph`, `res/system/media`, `res/system/audio`, `res/system/network`, `res/system/service`, `res/system/share`, `res/system/supervisor`, and `res/system/volume`.
- `res/int` for portable intelligence contracts and mechanisms: `model` owns verified local model bundles, tensors, and native backends; `inference` owns generation, embedding, transcription, speech, and provider contracts; `perception` owns interpretation and observation types; `realtime` owns duplex session types. Providers, models, credentials, and policy are chosen by consumers.
- `pkg/ext-go/engine` for the pure-Go engine, the oracle: the plugin contract, sessions, guests, packages, bridges and tooling (standard library only).
- `pkg/ext-go/c2goengine` for the C engine's portable core translated to Go by ccgo (no cgo, no C compiler). The translation is generated per platform by its `tools` module and held against the pure-Go engine, the oracle, by `engine/plugintest/enginetest`; it depends on `engine` only.
- `pkg/ext-go/cgoengine` for the Go binding of the C engine, with a pure-Go build of the same API and the C ABI loader.
- `pkg/ext-go/wasm` for the engine-facing WebAssembly backend; it depends on `pkg/ext-go/engine` and the reusable runtime in `res/web/wasm`.
- `pkg/ext-go/hashicorp` for the HashiCorp go-plugin backend.
- `pkg/ext-go/inference` for the `res/int/inference` provider contract over the Ext plugin engine.
- `pkg/ext-go/model` for the `res/int/model` tensor-provider contract over the Ext plugin engine.
- `pkg/ext-go/perception` for the `res/int/perception` host and guest contract over the Ext plugin engine.
- `pkg/ext-go/realtime` for the `res/int/realtime` duplex session contract over the Ext plugin engine.
- `pkg/ext-go` for the umbrella module that requires the plugin engines, `res/web` and `res/system`; it holds code that needs several engines, such as `pkg/ext-go/core`, which chooses between the pure-Go `engine`, `cgoengine` and `c2goengine` (the only place that knows all three).

Plugin-specific runtime bindings, including HashiCorp go-plugin and the
WebAssembly `engine.Backend`, belong inside the plugin library
(`pkg/ext-go/hashicorp`, `pkg/ext-go/wasm`). The reusable WebAssembly mechanism
belongs to `res/web/wasm` and never imports the plugin engine; `pkg/ext-go/wasm`
adapts it. Neither is a product adapter. Keep the shared host/guest contract
independent of backend choice so existing and future plugin implementations can use it.

Product adapter engines and stores that are reused across adapters
(`adapters/chromium` module, engine code in `adapters/chromium/engine`, `adapters/firefox/engine`, `adapters/safari/native`,
`adapters/keychain/store`, `adapters/credman/store`, and
`adapters/secret_service/store`) are their own Go modules, listed in `go.work`.
ctx does not import them: it loads adapters as installed ext packages. Product adapter
executables use the same plugin library. `pkg/ext-go/engine` owns the
adapter process protocol and adapter descriptor helpers (`engine/host/adapter`), and holds the pure-Go package store and host (`engine/host`, `engine/host/store`). Adapters and other Go
programs import `pkg/ext-go/cgoengine`, the Go entry point to the engine: it
re-exports the `engine` data model and serves sessions, guests, digests and
routes on the C engine (`ext_cengine` and cgo) or in pure Go, so `CGO_ENABLED=0`
builds keep working. There is no separate `pkg/plugin` module: its code is
`pkg/ext-go/engine`. Everything that runs plugins on a machine is grouped under `engine/host`: `host` opens an
installed package, and its subpackages `store` (the installed package store), `process` (starting the
child), `adapter` (the host-side adapter manager), `interop` (route resolution) and `nativego`
(loading a Go plugin built as a shared library). What a plugin implementation uses is grouped under
`engine/guest`: `author`, `capability`, `instance` and `stream`; the `Guest` type stays in the root
package beside `Session`. How frames travel is grouped under `engine/transport` (`jsonline`, `inprocess`, `bridge`) and how values
are checked under `engine/validate` (`schema`, `jsonschema`). The groups are layered: `transport` and
`validate` import neither `host` nor `guest` nor each other, and `host` and `guest` never import each
other (`src/ctx/internal/arch` enforces it). `package` (declared name `packagekit`, since `package` is a Go keyword) and `plugintest` stay at
the top because both sides use them. `cgoengine/packages` covers manifests over the
engine, and `cgoengine/host` and `cgoengine/host/store` are engine-routed variants of `engine/host` and
`engine/host/store` with an identical exported API (a rule `src/ctx/internal/arch` enforces), so
swapping the import never needs a code change.
`packagekit` has no dependency on `res/system`; joining a package plan to the host
graph is `plugingo.PlanGraph` in the umbrella module. Runtime backends
(`pkg/ext-go/hashicorp`, `pkg/ext-go/wasm`) and the C ABI loader in
`pkg/ext-go/cgoengine/cshared` implement `engine.Backend` and import
`pkg/ext-go/engine`. Hosts import `pkg/ext-go/engine/host/adapter`, which
owns manifests, the installed store, trust and invocation; a product keeps only
its own selection and commands.

A published Go module contains only its own directory. `pkg/ext-go/cgoengine`
therefore carries the C sources it is built from as `_csrc`, a generated copy of
`lib/plugin` made by `scripts/cgoengine-sync.sh`; `TestCSourcesMatchCanonical`
fails when the copy drifts. `lib/plugin` stays the canonical home that the iOS,
Android, Rust and Zig bindings build from: edit it, run the script, commit both.

Language SDKs and bindings belong under `pkg/<language>/` (Go, Rust, Zig and
TypeScript), with language-specific embedding examples alongside them. The
mobile SDKs are named for their platforms because they are native to them:
`pkg/ext-ios` (Swift, with Objective-C through the C headers) and
`pkg/ext-android` (Java, with Kotlin through the same API). Each is the one package an app adds
for a platform and ships both the plugin code (guest SDK, WebView and WAMR hosts), which is
written there, and the web code (the cookie, userscript and bookmarklet rules), which is not:
`res/web` is a Go module and also the iOS module (`res/web/ios`, the SwiftPM packages `ExtWeb`
and `ExtWebKit`) and the Android module (`res/web/android`, the Java packages
`io.github.webong.ext.web` and `io.github.webong.ext.web.android`) of the same web library.
Portable rules are checked against one set of fixtures; native workspace targets and source sets
remain separate from those portable rules. The mobile modules in `res/web` import nothing of
the plugin code or the C engine and build and test with Swift or a JDK/Android SDK
(`scripts/web-ios.sh`, `scripts/web-android.sh`, `scripts/web-differential.sh`).
The Go-only `res/web/wasm` package imports wazero but no plugin code;
`pkg/ext-go/wasm` supplies that binding. `pkg/ext-ios`
reaches `res/web/ios` through the links `Sources/ExtWeb` and `Sources/ExtWebKit` (a release stages
real copies), and `pkg/ext-android` compiles the portable and Android source sets from
`res/web/android` into its AAR. Edit web code in `res/web`, never in
the SDKs. `res/system/clipboard` follows the same ownership model: its Swift
target is linked into `pkg/ext-ios`, and its portable and Android Java source
sets are compiled into the `pkg/ext-android` AAR. Edit clipboard code in
`res/system/clipboard`. `res/system/network` follows that ownership model: its
Swift target is linked into `pkg/ext-ios`, and its Java source sets are compiled
into the `pkg/ext-android` AAR. Edit those implementations in `res/system`.
`res/system/network` exposes local state only.
`res/system/share` follows the same ownership model for mobile share and
keyboard extensions: its Swift target ships through `pkg/ext-ios` as part of
`ExtSystem`, and its Java source sets compile into the `pkg/ext-android` AAR.
The Android share and keyboard binding classes live in
`res/system/android/src/android`, beside the portable model. `res/system/service`
owns portable per-user service definitions and their non-overwriting
publication; the macOS launchd and Windows Task Scheduler backends are platform
implementations in `res/system/service`, while `systemctl --user` is a
selectable native tool owned by `adapters/systemd`.
`res/system/audio` owns portable device observation and bounded PCM stream
contracts; `pactl`/`parec`/`pacat`, `aplay`/`arecord`, `grim`/`wtype`/`ydotool`, `import`/`xdotool`,
and `ffmpeg` are selectable native tools owned by `adapters/pulseaudio`,
`adapters/alsa`, `adapters/wayland`, and `adapters/x11`. `adapters/pipewire`
implements the same contract with `pw-dump`, `pw-record`, and `pw-play`.
PipeWire and PulseAudio are separate adapters; no library selects between them
by name. `res/system.DesktopDriver` is the portable seam those adapters
implement, and the shared library never selects a driver by name.
`res/system` may also perform bounded local shell mechanisms such as capture,
media playback/capture and portable media framing, native input, volume snapshot/restore, and process
supervision. Remote clients, service discovery, routing services, authorization,
approval UI and consumer policy belong to consumers.
Keep
`pkg/ext-go` focused on the contract, the engine API and runtime backends. The Go-to-C guest binding belongs in
`pkg/ext-go/cgoengine/cshared/guest`. The language-neutral C ABI header
(`lib/plugin/include/ext_plugin.h`) belongs to the engine; its constants and wire
envelope (`cshared/abi`) and the runtime loader (`cshared`) live under
`pkg/ext-go/cgoengine/cshared`, inside the `cgoengine` module. `res/web/wasm`
is a package of the `res/web` module, not a nested module. `pkg/ext-go/wasm`
remains a separate module because it binds that reusable runtime to the plugin
engine without reversing the dependency into `res/`.

Moving a library into or out of `pkg/` changes public import paths. Update
`AGENTS.md`, the docs, and the language SDKs together, and verify with
`go build ./...` and `go vet ./...`.

Mobile browser workspaces are platform implementations of the public `res/web`
API, not product adapters. WKWebView code lives in `res/web/ios/Sources/ExtWebKit`
and is exposed by `pkg/ext-ios`; Android WebView and optional AndroidX
document-start integration live in `res/web/android/src/android` and are compiled
into the `pkg/ext-android` AAR. Portable reviewed-userscript preparation stays in
the portable targets and source sets. Keep Expo, app identity, storage policy,
and UI out of these libraries. Native changes are tested with
`scripts/web-workspace-ios.sh` and `scripts/web-workspace-android.sh`.

The same rule applies to an application-owned WebView2: its document-script and
profile-extension implementation lives in `res/web/windows`, not in an Edge or
Chromium product adapter. The caller supplies source already prepared by the
portable userscript tooling and owns persistence, profile choice and approval.
