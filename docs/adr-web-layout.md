# What `res/web` provides, where the extension library lives, and the host module

Status: decided 2026-11-03.

## The question

`res/web` grew by adding packages: eleven Go packages at one level, three non-Go
modules beside them, and six guides at the module root. The browser-extension
JavaScript library sat in `pkg/ext-web-js` as a staging copy that was meant to leave
for another repository. Ext should own that library, and the layout should say what
`res/web` is for.

## What `res/web` provides

| It provides | Where |
| --- | --- |
| A browser-management kit for hosts and browser adapters | `host/contract`, `host/manage`, `host/artifact/extension` |
| Web artifacts on every runtime | `host/artifact`, `ios`, `android`, `windows`, and `pkg/ext-web` |
| A way to run web content | `host/engine` (`bundle`, the web engine, and `wasm`) |

Every browser adapter and ctx import the first group. `wasm` is used by
`pkg/ext-go/wasm` and `adapters/wasm`, and `bundle` by one example. The browser
adapters do not require wazero in their `go.mod`, so keeping `wasm` in the module
costs them nothing.

## Decision

1. **Ext owns the extension JavaScript library, and it lives in `pkg/ext-web`.**
   It is mechanism, not product UI, so it follows the split in which ext holds
   reusable mechanisms and products such as Cymonkey and Xallet consume them.
   `pkg/` already holds packages named for the platform they run on (`pkg/ext-ios`,
   `pkg/ext-android`), and code that runs inside a browser extension is one more
   platform. The source lives directly in `pkg/ext-web`: `res/web` has no source
   module for it and there is no staging link. It is not a plugin SDK (`pkg/ext-ts`
   is) and imports neither the plugin engine nor any Go code.
2. **It is not an adapter.** Browser installation, profile discovery, signing and
   activation stay in `adapters/<browser>`. `res/web/host/artifact/extension` inspects
   and packages artifacts.
3. **It stays private for now,** under the npm name `ext-web`. A registry and a
   version are chosen later, with a release tag of its own like the mobile SDKs.
4. **`res/web` is a container with no Go code at its top.** Its Go module is
   `res/web/host`, with the module path `github.com/webong/ext/res/web/host` and
   tags `res/web/host/vX.Y.Z`. The old module path `github.com/webong/ext/res/web`
   keeps its tags `v0.1.0` to `v0.6.0` and is never tagged again.
5. **Inside `host`, packages are grouped by role.** `contract` is the shared
   vocabulary. `manage` holds `cookie` (the old root package), `sqlite`, `guest`,
   `discovery` and `policy`: the host side of reading and managing a browser.
   `artifact` holds `extension`, `userscript` and `bookmarklet`. `engine` holds
   `bundle` and `wasm`. The group directories hold no Go files. `artifact` imports
   only `contract`, `engine` imports nothing else in the module, and `manage` may
   import `contract` and `artifact`.
6. **`contract` imports nothing else in the module.** It had one import of
   `extension`, for `extension.InstallResult` in the progress-line helpers. Only
   `guest` used those helpers, so they moved to `artifact/extension` beside the type.
7. **`ios`, `android` and `windows` stay beside `host`.** Grouping them under a
   `webview` directory would change 17 files outside `res/web`, two symlinks in
   `pkg/ext-ios` and the SwiftPM, NuGet and Android release paths, and the host
   move does not need any of it.
8. **`docs` and `testdata` stay at the top of `res/web`.** `testdata` holds the
   fixtures that Go, Swift and Java all run. `docs` holds the guides for the Swift
   and Java mobile modules, which sit beside it and link to it; renaming it would
   change those links and gain nothing. The fixtures are therefore outside the
   `host` module zip. The Go tests that read
   them find them through `host/internal/fixtures`, which fails in a checkout when
   they are missing and skips in a copy of the module without the repository.

## Alternatives considered

- **Keeping the library in `res/web/js`.** Rejected: it put a Node package inside a
  container of Go and native modules, and `pkg/` already has the platform-named
  packages.
- **Another name for `manage`.** `browser` collides with the alias ctx already uses
  for the cookie package, and `adapterkit` hides what `cookie` and `sqlite` are for.
- **A verified copy of the fixtures inside `host`,** like `cgoengine`'s `_csrc`. It
  makes the tests self-contained but duplicates about twenty files that three other
  runtimes also read.
- **The npm name `@ext/web-js`.** Nothing is published under the `@ext` scope, but
  whether the scope can be owned could not be checked, and the unscoped `ext-web`
  matches the directory.

## Consequences

- This is a release round. `res/web/host` is a new module whose first tag is
  `v0.1.0`; the 23 modules that required `res/web` required `res/web/host` at that
  version before it was tagged, so a module could not resolve it from the proxy until
  the round pushed the tag. The release scripts take modules as directories and
  needed no change. See [module versioning](module-versioning.md).
- `src/ctx/internal/arch` checks the library's location and independence, the host
  module's path, its empty top and group directories, and the layering above.
- A workflow runs the library's tests, TypeScript declaration check and example
  build, then packages the built examples with `res/web/host/artifact/extension`
  (`TestExtWebExamplesPackage`), so a manifest the Go tooling cannot read fails
  there. The theme example is built by the library's own `createThemeManifest`, so
  that test covers it. Browser smoke tests need a browser and stay manual; see
  [the compatibility checks](../pkg/ext-web/COMPATIBILITY.md).
- The userscript registry repeats the Go rule for an ID, so its tests run the ID
  cases of the shared `record.json` fixtures. The registry also bounds the browser
  registration ID, which is the ID behind a namespaced prefix, to 128 characters;
  the Go rule allows an ID of 128 characters, so the longest ID the registry can
  register depends on its namespace. That limit is kept on purpose: it fails
  closed before anything is registered, no check records what the browsers
  accept, and a hashed ID would need a synchronous digest that the web platform
  does not offer. It is documented in the library README and declarations.
- Not done yet: installing the packaged examples through an adapter into a real
  browser, and a release tag, workflow, registry and version for `pkg/ext-web`.
