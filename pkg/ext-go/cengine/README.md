# cengine

`github.com/webong/ext/pkg/ext-go/cengine` is the C side of every library of `lib/`: it serves the C
drivers that [`core`](../core) picks between (purego, cgo and ccgo). `cgoengine` and `c2goengine` stay
their own modules and register with it, so a program downloads the ccgo translation only when it
imports it. See [the library opener decision record](../../../docs/adr-library-opener.md).

| Package | What it does |
| --- | --- |
| `library` | The opener purego needs. It loads a C shared library (`.so`, `.dylib`, `.dll`) once per canonical path, never unloads it, and binds its exports to Go function variables through [purego](https://github.com/ebitengine/purego), the same with and without cgo, on Linux, macOS and Windows for amd64 and arm64; on Windows it never searches the working directory for dependent DLLs. It knows no library by name. `Citation` is the pin of a library's release, and `Provide` and `OpenProvided` load a copy a program carries in its binary (what fetch writes for purego). `BindInterface` binds a library's interface (`<prefix>_open`, `_call`, `_close`), whose `CodeError` names each code. `Describe` reads the library's description of its interface (`describe` on handle 0, such as lib/plugin's `interface.json`) and drives any library from it with no Go of its own: `Open` an object by kind with `Handlers` for its events, `Call` (or the generic `Call[Out]`) an operation by name, and `CallWith` for the events a call raises (its progress), which are tagged to it and delivered before it returns; `ServeEvents` is the event loop underneath. `Browse`, `Reserve` and `Borrow`, the run-time path, come next. |
| `fetch`, `bind` and `libcmd` | `ext bundle fetch`: a project brings a C library in before it builds. `fetch.Plan` resolves every target/driver pair to a pinned asset without I/O; `Fetch` uses that same plan. It reads the library's citation, downloads each target's archive, checks its SHA-256, and writes a Go package the project blank-imports: headers and a static library with `${SRCDIR}` link flags for cgo, the translated source for c2go, or an embedded shared library for purego. `ext bundle bind` (`bind`) writes a typed Go binding of a library from the description of its interface: a type per object, a method per operation, handler structs for its events and struct types where the schemas name properties, over `library.Describe`. ext-go has no command: `libcmd` is the package behind ext's builtin `ext bundle`, so a project runs `go get -tool github.com/webong/ext/cmd/ext` and `go tool ext bundle fetch -lib <name>` / `go tool ext bundle bind -lib <name>` from `go generate`; see [the ADR](../../../docs/adr-library-opener.md). |
| `cguest` | Loads C ABI guests (the `ext_plugin_guest_*` exports, with `cguest/abi` for the ABI's constants) once per canonical path, never unloads them, and exchanges frames with them as bytes, one call at a time. It knows nothing of the plugin protocol: [`cguest/backend`](cguest/README.md) makes it a plugin backend. |
| `cguest/backend` | Adapts the loader to `core/plugin.Backend`, with Ext protocol validation, deadlines and errors. |
| `cguest/guest` | Exports a Go plugin guest through the C ABI for `-buildmode=c-shared`. |
| `plugin`, `plugin/host` | Go policies and transports over C-owned sessions and registry routes, including process and event backends. |
| `ledger`, `ledger/supervisor` | Bind the canonical C ledger and its supervisor using shared core contracts. |

The module requires purego and `core` for shared contracts.
The generic loader, library opener and build tooling do not import the plugin
engine. Loading a guest through purego needs no C toolchain; building a Go
c-shared guest requires cgo.

Calls go one way, from Go into C: purego's callbacks are limited in number and platform, so a C API
meant for `library` takes no function pointer it would call on a Go program's behalf.

The module path is new. The `ext_cengine` build tag and the old module `pkg/plugin-go/cengine` name the
C plugin engine binding, which is now [`cgoengine`](../cgoengine).
