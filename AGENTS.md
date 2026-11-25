# Adapter ownership

Anything specific to a product, provider, browser, or native tool belongs in its
adapter package under `adapters/<name>/`. This includes native protocols,
installation and activation, executable and profile discovery conventions,
storage formats, signing tools, store URLs, settings paths, and diagnostics.

Ext libraries own generic adapter contracts, validation, package trust, and
invocation. CTX, CTN and CTO are products built on Ext and installed as Ext packages; each
product owns its own selection and command dispatch. Adapters are ext adapters,
not owned by the interface invoking them. `ext` is the main installer and combined
entry point and the only public executable; the products consume declared capabilities
through it. Current adapter management uses `ext package` or `ext ctx adapter`, backed by
`pkg/ext-go/engine/plugin/host/adapter/adaptercmd`; `ext ctn adapter ls` is read-only. See
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

`pkg/ext-web` is ext's JavaScript/TypeScript package for code that runs inside a
browser extension. Ext owns it; Cymonkey and Xallet consume it. Like `pkg/ext-darwin` and
`pkg/ext-android` it is named for the platform it runs on, and unlike `pkg/ext-ts` it
is not a plugin SDK. Its extension-embedded runtime messaging, in-browser
management, themes, and `chrome`/`browser` compatibility are not adapters.
External browser installation, profile discovery, signing, and activation
remain adapter-owned, and `res/web/host/artifact/extension` inspects and packages the
extensions it builds. See [the plan](pkg/ext-web/PLAN.md).

# Public libraries

`pkg/` is the home for reusable libraries. Adapters, CTX, CTN, and other
ecosystem consumers build on them; `internal/` stays private to its owning
product and must not be imported by consumers.

The repository root is only the workspace: it holds `go.work` and no Go module or
package. The public binary release uses `ext-vX.Y.Z`; it is not a Go
module version. Bare `vX.Y.Z` tags are not used. The only bare tags are the `X.Y.Z` tags of one
distribution branch, `dist/ext-ios`, for SwiftPM; the others are tagged `<name>-X.Y.Z` (`dist/ext-macos` tags
`ext-macos-X.Y.Z`, so macOS consumers pin it by branch or revision, and `dist/ext-zig` tags `ext-zig-X.Y.Z`).
Only the release workflow writes them and they are never merged; see [distribution branches](docs/module-versioning.md#distribution-branches).
`tests/` is a container for test-only Go modules that belong to no single library: `tests/crosslang` runs
every language's guests and hosts against each other, with `pkg/ext-go` as the host side. Its modules are
listed in `go.work`, are never tagged or published, and no library imports them. `src/` is a container like `res/`; the context manager is the module
`src/ctx` (with its private code in `src/ctx/internal`), the content manager is
`src/ctn`, and containment is `src/cto`. Each product exports a reusable Go
library and ships a private Ext package guest. Only `cmd/ext` ships a public binary. A product
never imports another product module. The standalone `src/cto/sandbox` contract
is available to its consumers. Shared implementation code goes in `pkg/` or `res/`.
`examples/` is not a Go module: it holds only samples that need no Go build (a shell adapter and an Android
consumer app). Go examples live in the library they show, as an `examples/` directory inside that module:
`pkg/ext-go/examples` for plugins (the umbrella requires every backend), `pkg/ext-go/engine/ledger/examples` and
`res/web/host/examples` for those libraries. The product modules export `ctx.Run`,
`ctn.Run` and `cto.Run` for Go consumers; the private guests in each product's
`plugin/` directory adapt those functions to `ext.command/v1`. Ext does not import
the product modules. Their module paths include `src/`, so external Go consumers
can import them directly once their module versions are tagged and published.

The layers, the rules every `res/` module follows (fixtures as the contract, capability
reporting, one vocabulary of error codes, bounds) and the language support tiers are in
[the architecture ADR](docs/adr-architecture.md).

`res/` is a container for reusable modules rather than a package. Each
subdirectory is its own Go module, except `res/web`, which is a container too,
and nothing lives at that level: no Go package
and no repository-wide policy. Keep container directories free of shared logic
and keep repo-wide tests in `src/ctx/internal/arch`. `res/web/host` depends on wazero
for its protocol-neutral WebAssembly runtime and never on the plugin engine;
`res/system` depends on `golang.org/x/sys`. The ledger recorder (`pkg/ext-go/engine/ledger/recorder`) requires `res/system` for the collector types; `res/system` never requires
it (`src/ctx/internal/arch` enforces it).

`res/web` has no Go code at its top. Its Go module is `res/web/host`
(`github.com/webong/ext/res/web/host`, tagged `res/web/host/vX.Y.Z`); the module path
`github.com/webong/ext/res/web` is never tagged again. Inside `host`, packages are
grouped by role and the group directories hold no Go files of their own: `contract`
(the shared vocabulary, which imports nothing else in the module), `manage` (`cookie`,
`sqlite`, `guest`, `discovery` and `policy`: the host side of reading and managing a
browser), `artifact` (`extension`, `userscript` and `bookmarklet`) and `engine` (`bundle`
and `wasm`). `artifact` imports only `contract`, `engine` imports nothing else in the
module, and `manage` may import `contract` and `artifact`; `src/ctx/internal/arch`
enforces it. The iOS, Android and WebView2 modules (`ios`, `android`, `windows`) are
siblings of `host`. `testdata` at the top holds the fixtures every runtime runs, and
`docs` the guides for the mobile modules.
The shared fixtures are outside the `host` module, so its Go tests find them by
walking up (`host/internal/fixtures`) and skip when run without the repository.

`lib/` is a container for native libraries that several language bindings
share. Nothing lives at that level. `lib/plugin` is the portable C plugin
engine, built with CMake; all its public names are in the `ext_plugin_` name space with the side next
(`ext_plugin_host_*` for the host ABI, `ext_plugin_guest_*` for the guest ABI and the C guest API,
`ext_plugin_store_*`, `ext_plugin_services_call`; see its README): it is not a Go module, has no `go.mod` and is not
listed in `go.work`. Its only third-party code is in `lib/plugin/vendor`,
pinned by version and SHA-256 with its licence beside it: the yyjson parser and
the inflate decoder of miniz (`vendor/miniz`), which the package store's
install from a zip or tar.gz archive uses. Vendored sources stay unmodified and
their symbols are renamed at build time; adding one is a decision for the user. The language bindings (`pkg/ext-go`, `pkg/ext-rust`, `pkg/ext-zig`, `pkg/ext-ts`,
`pkg/ext-python`) and the mobile SDKs (`pkg/ext-darwin`, `pkg/ext-android`) build on it.
The JSON-line guest SDKs (`pkg/ext-python`, `pkg/ext-ruby`, `pkg/ext-php`, `pkg/ext-lua`, and
`ExtIO` in `pkg/ext-dotnet`) are written in their own language only: they speak `ext.plugin/v1` as JSON lines over stdio,
need no C or native build, and are checked against the same fixtures. A host binding for one of
them binds `lib/plugin` through that language's own native extension mechanism, not a generated
wrapper, as the Python `ext_io.host` does with `ctypes`, the Ruby `ExtIO::Host` with a native
extension, the Lua `ext_io.host` with a C module (`ext_io.host.core`) and the .NET `ExtIO.Host`
(`pkg/ext-dotnet`) with P/Invoke, each loading the engine's shared library at run time, and the PHP
`ExtIO\Host` with PHP FFI; importing the guest never loads it. Every host runs
the shared host cases (`lib/plugin/testdata/v1/host-cases.json`, `scripts/host-cases.sh`), which
the pure-Go host is held to as well. See [hosts in every language](docs/adr-hosts-in-every-language.md).
The C engine in `lib/plugin` is the main plugin engine: every binding outside Go hosts on it. A new
engine or host feature (sessions, reverse calls, routing, the package store) is built in C first, with
its fixtures in `lib/plugin/testdata`, and then reaches `pkg/ext-go/cgoengine`, a regenerated
`pkg/ext-go/c2goengine` and the other bindings. Go binds the C libraries like every other language
(decided 2026-11-23, [the library opener ADR](docs/adr-library-opener.md), decision 8): Go hosts and the
Go ledger reach `lib/plugin` and `lib/ledger` through their library interfaces with the cgo, c2go and
purego drivers, and only the Go guest SDK and the shared data model stay pure Go. Until their users move,
the pure-Go host in `pkg/ext-go/engine` (and the pure-Go build of `pkg/ext-go/cgoengine`) keeps
`CGO_ENABLED=0` programs working and is retired after; build nothing new on it. Where the two
disagree, the C engine and the fixtures decide. See
`docs/plugin-engine-parity.md`. A change to the plugin contract or the package manifest lands in both
implementations, and the conformance checks compare the two.
`lib/ledger` is the ledger (the graph store, recorder and supervisor) in C, built with CMake like `lib/plugin` and, like it, not a Go
module: the store now (memory and file backends, the optional SQLite backend on `tables.json`), the
reader and the recorder with its per-kernel collectors next. It has no third-party code at all (its
JSON, SHA-256 and CRC-32 are its own) and shares nothing with `lib/plugin`. It is held to the
`lib/ledger/testdata` fixtures. It is the main ledger implementation; `pkg/ext-go/engine/ledger` is the
pure-Go fallback that follows it, as `engine/plugin` follows `lib/plugin` (see
[the architecture ADR](docs/adr-architecture.md#resgraph-storage-and-libledger)).

Each reusable library under `pkg/` is its own Go module, not a package inside
one repository-wide module. The root `go.work` keeps the repository building as
a single workspace while every module stays independently buildable. Keep each
library's dependencies pointing inward so it never depends back on its consumers;
`src/ctx/internal/arch` enforces that direction.

`pkg/ext-go/` is a tree of modules, not one module, so an application takes
only the parts it needs: `engine` (portable plugin code plus the C-backed ledger API), `cgoengine` (the C
engine binding, with a pure-Go build of the same API), `wasm` (the plugin binding
for `res/web/host/engine/wasm`), and `hashicorp` (go-plugin, grpc), plus
`pkg/ext-go` itself as a thin umbrella module that requires them at matching
versions. CTN owns the inference, model, perception, and realtime bridges as
nested modules under `src/ctn`. Nested modules are excluded
from the module that contains them, so an import path must belong to exactly one
module and never move between modules at a version a consumer may still resolve;
published paths are not reused by a different module.

Runtime backends that own a heavy runtime stay outside the engine module:
`pkg/ext-go/hashicorp` is a separate module for go-plugin/grpc, while
`pkg/ext-go/wasm` binds the protocol-neutral wazero runtime in `res/web/host/engine/wasm`
to `engine.Backend`, and `pkg/ext-go/yaegi` interprets a guest shipped as Go source
(entrypoint runtime `go-source`, a tree artifact) in-process with yaegi, limited to the
standard library packages the host allows and the ext guest packages. A backend never
depends on `pkg/ext-go/cgoengine`, so a wazero, grpc or yaegi user needs no C toolchain and the engine binding is not in their
module graph. Tests that need
a backend belong in that backend's module, and tests or commands that need both
a backend and the engine belong in the umbrella module, not in `engine`, so
the core stays free of every runtime. `pkg/ext-go/engine/plugin` uses the standard
library only (its nested module `engine/ledger/recorder` is outside it). `src/ctx/internal/arch` enforces both rules.

When adding a library module, list it in `go.work`, give it a `go.mod`, and add
matching `require` directives to a product module only if that product imports it
(adapters are loaded as ext packages, so product `go.mod` files never list them). Do not add
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

- `res/web` for portable web contracts and workflows: browsers, WebAssembly inspection/execution through wazero (`res/web/host/engine/wasm`), and the web engine that runs web content through browsers. It is also the iOS and Android module of the cookie, userscript and bookmarklet rules (`res/web/ios`, `res/web/android`), which `pkg/ext-darwin` and `pkg/ext-android` ship, and owns the application-hosted WebView2 implementation in `res/web/windows`. Its Go module is `res/web/host`.
- `pkg/ext-web` for the JavaScript/TypeScript library that runs inside a browser extension: peer messaging, in-browser management, workspace helpers, a userscript registry, a page bridge and themes. It is built and tested with Node, is `private: true` under the npm name `ext-web`, and depends on neither the plugin engine nor any Go code.
- `lib/plugin` for the portable C plugin engine shared by the language bindings under `pkg/` (`ext-go`, `ext-rust`, `ext-zig`, `ext-ts`) and the mobile SDKs. The JSON-line guest SDKs (`ext-python`, `ext-ruby`, `ext-php`, `ext-lua`, and the .NET `ExtIO`) do not link it; the Python and Lua hosts (`ext_io.host`), the Ruby host (`ExtIO::Host`, gem `ext-io-host`), the PHP host (`ExtIO\Host`, through FFI) and the .NET host (`ExtIO.Host`, `pkg/ext-dotnet`) load its shared library at run time.
- `res/system` for the local operating-system substrate used to extend graphical and textual shells: host, shell, filesystem and volume mechanics, clipboard, credential integration, local interface/address/route/resolver observation, web view and process discovery, native surfaces, accessibility, permission state, bounded capture and media playback/capture, and atomic native input. It owns mechanisms, not authorization, remote control, routing services or consumer policy. The module has no root package; its packages sit in three groups, which are plain directories: `service` (what the host provides, looked up and used: `host` for shells, filesystems, web views and resource capacity, `network`, `process`, `program`, `credential` and `user` for per-user service definitions), `capture` (content and events crossing the device edge: `surface` for displays, windows, accessibility, permission, capture and input, `audio`, `clipboard`, `share` and `media`) and `device` (`volume` for snapshots and private directories, `image` for snapshot archives). Each package owns its own iOS, Android and Windows code (the .NET libraries are `capture/audio/windows` and `capture/clipboard/windows`). The Windows .NET libraries of `res/` (those two, `res/web/windows` and `res/int/model/windows`) ship as NuGet packages named like their assemblies, released together under the tag `ext-windows-vX.Y.Z`, the Windows platform package (the counterpart of `ext-ios` and `ext-android`); the .NET guest SDK `ExtIO` and plugin host `ExtIO.Host` (`pkg/ext-dotnet`), a language package, are released on their own as `ext-dotnet-vX.Y.Z`.
- `lib/ledger` for the ledger in C, the main implementation: the store over its file layout (`lib/ledger/testdata/store/v1/ledger.json`) or SQLite, held to the same fixtures.
- `res/int` for portable intelligence contracts and mechanisms: `model` owns verified local model bundles, tensors, and native backends; `inference` owns generation, embedding, transcription, speech, and provider contracts; `perception` owns interpretation and observation types; `realtime` owns duplex session types. Providers, models, credentials, and policy are chosen by consumers.
- `src/cto/sandbox` for CTO's sandbox contract shared with `github.com/webong/workload` ([the sandboxing ADR](docs/adr-sandboxing.md)): boundary levels, policy and lifecycle vocabulary, grants, capabilities, enforcement and reports, held to `src/cto/sandbox/testdata`. This nested module uses only the standard library and imports nothing else in ext, so runners outside ext can depend on it (`src/ctx/internal/arch` enforces it). CTO's local confinement mechanism lives in `src/cto/internal/confine`; workload owns its drivers. Consumers choose the policy.
- `pkg/ext-go/engine` for the Go APIs of the C libraries. `engine/plugin` remains the portable, standard-library-only plugin code during its migration. `engine/ledger` is the existing Go ledger API and now binds `lib/ledger` through `cengine/library`; do not create a sibling `pkg/ext-go/ledger` module. It owns the shared Go observation types and portable host-selection policy. CTX uses its C-backed `Bind`, `Library.OpenStore` and `NewGraph` path. The old Go memory store, supervisor and nested recorder remain compatibility code for unmigrated consumers; the legacy recorder aliases the shared model rather than duplicating it. The engine module therefore requires `cengine` and `res/system`, but portable plugin packages must not import them. CTX's private `graphapi` owns library location and the one-time read-only SQLite snapshot import into the C file store; ledger mechanics stay in C.
- `pkg/ext-go/c2goengine` for the C libraries' portable code translated to Go by ccgo (no cgo, no C compiler): `c2goengine/plugin` is lib/plugin's. The translation is not committed: the `tools` module generates it on each platform, the binding beside it (`internal/ccgo`) builds only with `-tags ext_c2go`, the ext-plugin release translates, tests against the pure-Go engine (`engine/plugin/plugintest/enginetest`) and publishes each platform's as the c2go driver's archive, and a project fetches it (`go tool ext bundle fetch -lib ext_plugin -driver c2go`); it depends on `engine` only.
- `pkg/ext-go/cgoengine` for the Go bindings of the C libraries in `lib/`: `cgoengine/plugin` binds lib/plugin, with a pure-Go build of the same API. It carries no C: a cgo build points `CGO_CFLAGS` at the engine's headers and `CGO_LDFLAGS` at its library, the ones `go tool ext bundle fetch -lib ext_plugin -driver cgo` brings into a project (in this repository, `lib/plugin/include` and a CMake build).
- `pkg/ext-go/core` for the switcher: `core/driver` picks how a program runs each library of `lib/`, go (the pure-Go `engine`) or one of the C drivers `cengine` serves (purego, cgo, ccgo), by name, by `EXT_ENGINE` or as the best available one ([the library opener ADR](docs/adr-library-opener.md)). It knows no library by name, uses only the standard library, requires no module, and has no root package, because the umbrella published the selection package at `pkg/ext-go/core` up to v0.12.0 (that package is gone: Go reaches lib/plugin through its description, `cengine/library.Describe` or a binding `ext bundle bind` generates).
- `pkg/ext-go/cguest` for the plugin backend that runs plugin guests built as C shared libraries, like `yaegi`, `hashicorp` and `wasm`: `cguest` speaks `ext.plugin/v1` over `cengine/cguest` (the frames, the frame limit, the default deadline, the descriptor and response checks, the engine's errors) and `cguest/guest` is the Go guest export (`-buildmode=c-shared`). It requires `engine` and `cengine`, never `cgoengine`. Running such a guest is not loading a library of `lib/`: the plugin contract plays no part in how the drivers reach `lib/plugin` or `lib/ledger`.
- `pkg/ext-go/cengine` for the C side of every library of `lib/`, knowing none of them and depending on no ext module (purego only): it serves the C drivers (purego, cgo and ccgo), and `cgoengine` and `c2goengine` stay their own modules and register with it, so a program downloads the ccgo translation only when it imports it. `cengine/library` is the opener purego needs: it finds, fetches, verifies and opens the built copy of the C library once per canonical path and binds its exports. `cengine/fetch` is how a Go project that uses ext-go brings a C library in before it builds: from the library's citation it downloads and verifies each target's archive and writes a Go package the project imports, for cgo (headers and a static library linked through `${SRCDIR}`), c2go (the translation) or purego (an embedded shared library); ext-go itself carries no C ([the library opener ADR](docs/adr-library-opener.md)). `cengine/bind` generates a typed Go binding of a library from the description of its interface (lib/plugin's `interface.json`). ext-go is a program SDK and has no command: `cengine/libcmd` is the shared command package behind ext's builtin `ext bundle fetch` and `ext bundle bind`, which a project runs as `go tool ext bundle ...` from `go generate` after `go get -tool github.com/webong/ext/cmd/ext`. `cengine/cguest` loads C ABI guests (the `ext_plugin_guest_*` exports) and exchanges frames with them as bytes, with `cguest/abi` for the ABI's constants; what the frames mean is `pkg/ext-go/cguest`'s. The module path is new; the `ext_cengine` build tag and the old `pkg/plugin-go/cengine` module name the C plugin engine binding, now `cgoengine`.
- `pkg/ext-go/wasm` for the engine-facing WebAssembly backend; it depends on `pkg/ext-go/engine` and the reusable runtime in `res/web/host/engine/wasm`.
- `pkg/ext-go/hashicorp` for the HashiCorp go-plugin backend.
- `pkg/ext-go/yaegi` for the Go source backend: a guest shipped as Go source, interpreted in-process by yaegi (`github.com/traefik/yaegi`); it depends on `pkg/ext-go/engine` and yaegi only, and the umbrella does not require it.
- `pkg/ext-go` for the umbrella module that requires the plugin engines, `res/web/host` and the ledger recorder; it holds code that needs several modules together, and the examples and benchmarks. No package chooses between the engines, and ext-go holds no binding of a library of `lib/`: `tests/crosslang` holds lib/plugin's generated binding and holds it to the differential checks through purego.

CTN owns the nested modules `src/ctn/inference`, `src/ctn/perception`, and
`src/ctn/realtime` for provider, perception, and duplex session contracts
over Ext plugin sessions. Each has its own module path and version tag.
CTN owns the public `model.tensor/v1` bridge in `src/ctn/model`. Cymonkey
and other peers can import it and open an installed provider directly through
Ext. The portable model types and inference interfaces live in `res/int/model`.

Plugin-specific runtime bindings, including HashiCorp go-plugin, the
WebAssembly `engine.Backend` and the yaegi interpreter, belong inside the plugin library
(`pkg/ext-go/hashicorp`, `pkg/ext-go/wasm`, `pkg/ext-go/yaegi`). The reusable WebAssembly mechanism
belongs to `res/web/host/engine/wasm` and never imports the plugin engine; `pkg/ext-go/wasm`
adapts it. None of them is a product adapter. Keep the shared host/guest contract
independent of backend choice so existing and future plugin implementations can use it.

Product adapter engines and stores that are reused across adapters
(`adapters/chromium` module, engine code in `adapters/chromium/engine`, `adapters/firefox/engine`, `adapters/safari/native`,
`adapters/keychain/store`, `adapters/credman/store`, and
`adapters/secret_service/store`) are their own Go modules, listed in `go.work`.
ctx does not import them: it loads adapters as installed ext packages. Product adapter
executables use the same plugin library. `pkg/ext-go/engine` owns the
adapter process protocol and adapter descriptor helpers (`engine/plugin/host/adapter`), and holds the pure-Go package store and host (`engine/plugin/host`, `engine/plugin/host/store`). Adapters and other Go
programs import `pkg/ext-go/cgoengine/plugin`, the Go entry point to the plugin engine: it
re-exports the `engine/plugin` data model and serves sessions, guests, digests and
routes on the C engine (`ext_cengine` and cgo) or in pure Go, so `CGO_ENABLED=0`
builds keep working. There is no separate `pkg/plugin` module: its code is
`pkg/ext-go/engine/plugin`. Everything that runs plugins on a machine is grouped under `engine/plugin/host`: `host` opens an
installed package, and its subpackages `store` (the installed package store), `process` (starting the
child), `adapter` (the host-side adapter manager), `interop` (route resolution) and `nativego`
(loading a Go plugin built as a shared library). What a plugin implementation uses is grouped under
`engine/plugin/guest`: `author`, `capability`, `instance` and `stream`; the `Guest` type stays in
`engine/plugin` itself beside `Session`. How frames travel is grouped under `engine/plugin/transport` (`jsonline`, `inprocess`, `bridge`) and how values
are checked under `engine/plugin/validate` (`schema`, `jsonschema`). The groups are layered: `transport` and
`validate` import neither `host` nor `guest` nor each other, and `host` and `guest` never import each
other (`src/ctx/internal/arch` enforces it). `package` (declared name `packagekit`, since `package` is a Go keyword) and `plugintest` stay at
the top because both sides use them. `cgoengine/plugin/packages` covers manifests over the
engine, and `cgoengine/plugin/host` and `cgoengine/plugin/host/store` are engine-routed variants of `engine/plugin/host` and
`engine/plugin/host/store` with an identical exported API (a rule `src/ctx/internal/arch` enforces), so
swapping the import never needs a code change.
`packagekit` has no dependency on the ledger; joining a package plan to the host
graph is `plugingo.PlanGraph` in the umbrella module. Runtime backends
(`pkg/ext-go/hashicorp`, `pkg/ext-go/wasm`) and the C ABI backend in
`pkg/ext-go/cguest` implement `engine.Backend` and import
`pkg/ext-go/engine`. Hosts import `pkg/ext-go/engine/plugin/host/adapter`, which
owns manifests, the installed store, trust and invocation; a product keeps only
its own selection and commands.

A published Go module contains only its own directory, and ext-go carries no C: `pkg/ext-go/cgoengine`
compiles against the headers and links the library a project fetched from the `ext-plugin` release
(`ext bundle fetch`, by the release's citation), given to cgo as `CGO_CFLAGS` and `CGO_LDFLAGS`; the
repository's own scripts give it `lib/plugin/include` and a CMake build of `lib/plugin`. `lib/plugin`
is the canonical home that the iOS, Android, Rust and Zig bindings build from. The Zig package
(`pkg/ext-zig`) carries the portable guest core the same way, as `csrc`, made by `scripts/ext-zig-sync.sh`;
`scripts/ext-zig-release.sh` fails when that copy drifts.

Language SDKs and bindings belong under `pkg/<language>/` (Go, Rust, Zig and
TypeScript), with language-specific embedding examples alongside them. `pkg/ext-web`
is not one of them: it is the package for code that runs inside a browser extension,
named for that platform like the mobile SDKs. The
mobile SDKs are named for their platforms because they are native to them:
`pkg/ext-darwin` (Swift, with Objective-C through the C headers, for iOS and macOS) and
`pkg/ext-android` (Java, with Kotlin through the same API). Each is the one package an app adds
for a platform. `pkg/ext-darwin` holds the sources of two Apple releases built from the same code:
`ext-ios` (the iOS and iOS Simulator slices, `ext-ios-vX.Y.Z`, `dist/ext-ios`) and `ext-macos` (the
macOS slice, `ext-macos-vX.Y.Z`, `dist/ext-macos`), each with a generated manifest that declares only
its platform; its own `Package.swift` stays multi-platform for development and CI. Both export the
same module names, so an app depends on one of them, or on `pkg/ext-darwin` from source. Each SDK ships both the plugin code (guest SDK, WebView and WAMR hosts), which is
written there, and the web code (the cookie, userscript and bookmarklet rules), which is not:
`res/web` is a container whose Go module is `res/web/host`, and it holds the iOS module (`res/web/ios`, the SwiftPM packages `ExtWeb`
and `ExtWebKit`) and the Android module (`res/web/android`, the Java packages
`io.github.webong.ext.web` and `io.github.webong.ext.web.android`) of the same web library.
Portable rules are checked against one set of fixtures; native workspace targets and source sets
remain separate from those portable rules. The mobile modules in `res/web` import nothing of
the plugin code or the C engine and build and test with Swift or a JDK/Android SDK
(`scripts/web-ios.sh`, `scripts/web-android.sh`, `scripts/web-differential.sh`).
The Go-only `res/web/host/engine/wasm` package imports wazero but no plugin code;
`pkg/ext-go/wasm` supplies that binding. `pkg/ext-darwin`
reaches `res/web/ios` through the links `Sources/ExtWeb` and `Sources/ExtWebKit` (a release stages
real copies), and `pkg/ext-android` compiles the portable and Android source sets from
`res/web/android` into its AAR. Edit web code in `res/web`, never in
the SDKs. `res/system/capture/clipboard` follows the same ownership model: its Swift
target is linked into `pkg/ext-darwin`, and its portable and Android Java source
sets are compiled into the `pkg/ext-android` AAR. Edit clipboard code in
`res/system/capture/clipboard`. `res/system/service/network` follows that ownership model: its
Swift target is linked into `pkg/ext-darwin`, and its Java source sets are compiled
into the `pkg/ext-android` AAR. Edit those implementations in `res/system/service/network`.
`res/system/service/network` exposes local state only.
`res/system/capture/share` follows the same ownership model for mobile share and
keyboard extensions: its Swift target ships through `pkg/ext-darwin` as `ExtShare`, and its Java source sets compile into the `pkg/ext-android` AAR.
The Android share and keyboard binding classes live in
`res/system/capture/share/android/src/android`, beside the portable model.
`capture/surface` (`ExtSystem`), `service/host` (`ExtHost`) and `capture/audio` (`ExtAudio`) follow the same
model: each owns the Swift and Java code for its own Go package. `res/system/service/user`
owns portable per-user service definitions and their non-overwriting
publication; the macOS launchd and Windows Task Scheduler backends are platform
implementations in `res/system/service/user`, while `systemctl --user` is a
selectable native tool owned by `adapters/systemd`.
`res/system/capture/audio` owns portable device observation and bounded PCM stream
contracts; `pactl`/`parec`/`pacat`, `aplay`/`arecord`, `grim`/`wtype`/`ydotool`, `import`/`xdotool`,
and `ffmpeg` are selectable native tools owned by `adapters/pulseaudio`,
`adapters/alsa`, `adapters/wayland`, and `adapters/x11`; `adapters/ffmpeg` owns recording on
macOS and Windows, where capture and input need no tool and are `surface.NativeDesktop`. `adapters/pipewire`
implements the same contract with `pw-dump`, `pw-record`, and `pw-play`.
PipeWire and PulseAudio are separate adapters; no library selects between them
by name. `res/system/capture/surface.DesktopDriver` is the portable seam those adapters
implement, and the shared library never selects a driver by name.
`src/cto/internal/confine` confines one local process to a `src/cto/sandbox` grant and then becomes the guest; its drivers (Seatbelt on macOS now, Landlock and seccomp on Linux, AppContainer and a job object on Windows next) are CTO platform implementations, not adapters. It never chooses a policy. `res/system` has no CTO dependency.
`src/cto/internal/app` owns `ext cto exec|check` and its `ext sandbox` alias, dispatched
through the installed CTO package. Each product's private `plugin/` entrypoint serves its command through the Ext guest SDK; Ext's host dispatch lives in `cmd/ext/internal/pkgcmd`. Product dispatch is not part of `pkg/ext-go`.
`res/system` may also perform bounded local shell mechanisms such as capture,
media playback/capture and portable media framing, native input, and volume snapshot/restore; process
supervision is `pkg/ext-go/engine/ledger/supervisor` (and `ext_ledger_supervisor_*` in `lib/ledger`). Remote clients, service discovery, routing services, authorization,
approval UI and consumer policy belong to consumers.
Keep
`pkg/ext-go` focused on the contract, the engine API and runtime backends. The Go-to-C guest binding belongs in
`pkg/ext-go/cguest/guest`. The language-neutral C ABI header
(`lib/plugin/include/ext_plugin_guest.h`) belongs to the engine; its constants and wire
envelope (`cguest/abi`) and the loader (`cguest`) live in `pkg/ext-go/cengine/cguest`, whose purego build opens
guests through the generic loader `pkg/ext-go/cengine/library`; the plugin backend over it is `pkg/ext-go/cguest`. The loader has a
cgo build and, for `CGO_ENABLED=0`, a purego build (Linux, macOS and Windows on
amd64 and arm64; elsewhere it returns `ErrUnsupported`). On darwin/amd64 both builds refuse a Go
c-shared guest, since two Go runtimes there share the TLS slot for g. purego is a dependency
of `pkg/ext-go/cengine` only, never of `pkg/ext-go/engine` or `cgoengine`, and both builds run the
cshared tests and the shared host cases (`scripts/cshared-purego.sh`). `res/web/host/engine/wasm`
is a package of the `res/web/host` module, not a nested module. `pkg/ext-go/wasm`
remains a separate module because it binds that reusable runtime to the plugin
engine without reversing the dependency into `res/`.

Moving a library into or out of `pkg/` changes public import paths. Update
`AGENTS.md`, the docs, and the language SDKs together, and verify with
`go build ./...` and `go vet ./...`.

Mobile browser workspaces are platform implementations of the public `res/web`
API, not product adapters. WKWebView code lives in `res/web/ios/Sources/ExtWebKit`
and is exposed by `pkg/ext-darwin`; Android WebView and optional AndroidX
document-start integration live in `res/web/android/src/android` and are compiled
into the `pkg/ext-android` AAR. Portable reviewed-userscript preparation stays in
the portable targets and source sets. Keep Expo, app identity, storage policy,
and UI out of these libraries. Native changes are tested with
`scripts/web-workspace-ios.sh` and `scripts/web-workspace-android.sh`.

The same rule applies to an application-owned WebView2: its document-script and
profile-extension implementation lives in `res/web/windows`, not in an Edge or
Chromium product adapter. The caller supplies source already prepared by the
portable userscript tooling and owns persistence, profile choice and approval.
