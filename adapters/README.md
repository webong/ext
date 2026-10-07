# Adapters

This directory contains every tool-specific integration maintained in the ctx
repository.

External Go adapters can use the public `github.com/webong/ext/adapter` process
parser and ship a platform-specific `.ctxadapter` archive. A bare ctx binary
installs that archive without Go; source builds use `ctx adapter build`.

`docker`, `podman`, `nerdctl`, and `apple` are maintained manager providers.
Their manifests declare `runtime = "manager"` and `surfaces = "shell"`;
each package owns its native CLI syntax, context discovery, validation, routing,
build behavior, and supported image and volume operations. The ctx core sees
only the common capability protocol.
Manifest `supports` declarations describe resource kinds independently of
runtime. For example, the Podman manager and AWS computer adapters both
declare `virtualizer` and `container`.
Docker also implements the versioned `observe` operation for the system graph;
the other providers continue to use their `list` fallback until they expose
additional context or resource observations.

The Docker, Podman, and nerdctl directories also contain tiny command shims. An
explicit adapter installation copies those launcher files into the binary
directory under the native command names so ordinary commands can be context-aware. The provider
implementations themselves remain installed and trusted under
`$EXT_HOME/adapters`.
The `internal/mod` Go package manages these ctx modifications: manifests,
installation, trust, and invocation. Provider behavior stays in `adapters/`.

`rancher_desktop`, `orbstack`, and `docker_desktop` are manager-app adapters.
They declare `supports = "virtualizer"` and implement app status, explicit
start/stop, and read-only diagnostics through each product's own CLI. They do
not replace the `docker` or `nerdctl` engine adapters, and they never claim
ownership of the global Docker CLI plugin links. The generic
`ctx manager app <adapter> <action>` route invokes these packages without
hard-coding product behavior in ctx core.

`firefox`, `zen`, `floorp`, `waterfox`, `librewolf`, `chrome`, `chromium`, `edge`,
`brave`, `vivaldi`, `opera`, `whale`, `arc`, `comet`, `dia`, `atlas`, `helium`, and
`safari` are browser providers. The built-in
`browser` context aggregates them while each provider owns application-specific
profile discovery, validation, launching, and declared browser share operations.
`ctx share:browser` bridges resources between trusted adapters using the
versioned JSON protocol in [the adapter API](../docs/adapter-api.md).
The optional release catalog and explicit source-installer selection provide a
separate share executable built from each browser adapter's `native` directory.
Portable request handling lives in `res/browser/guest`, with shared types and
validation in `res/browser/contract`. Shared policy export and executable
discovery helpers live in `res/browser/policy` and `res/browser/discovery`.
adapters provide SQLite access via `res/browser/sqlite`. They are Go libraries used by
adapters, not installable catalog entries. The browser-specific engines live in
`adapters/chromium/engine` and `adapters/firefox/engine`; their respective
families configure and reuse them.
The executable is part
of that adapter's trusted checksum; CTX core only routes
the declared operation and validates the shared envelope.
Explicit adapter selection updates the catalog and refreshes active browser
adapters with the packaged helper. A core-only update leaves installed browser
adapters in place; refresh the optional catalog when updating their helpers.

`kube`, `aws`, `gcloud`, `postgres`, `mysql`, and `php` are maintained computer-runtime
adapters. Every adapter has an `adapter.toml` manifest and implements ctx adapter
API v2.0. Only explicit adapter selection downloads or prepares maintained
packages in `$EXT_HOME/catalog/adapters`; `ctx setup` or `ctx adapter add` activates selected
packages under `$EXT_HOME/adapters` and records their checksum trust. A package
can declare `executable_windows` alongside its default executable; the native
core selects the platform implementation at runtime while keeping one manifest,
capability set, and trust record.

`keychain`, `secret_service`, and `credman` are separate, nonselectable
credential-store adapters for macOS, Linux, and Windows. They register the
`credential` share space and own their native store APIs and item identities.
The generic `ctx credential` command moves explicit item values through their
trusted processes without storing secrets in CTX configuration. Chromium
browser adapters declare platform-specific credential dependencies and request
supported cookie keys through CTX's trusted adapter protocol. They retain
browser-specific identities without importing store implementation packages.

`php` is the PHP interpreter adapter. It declares `supports = "interpreter"`,
observes installed Homebrew PHP formulae as graph contexts, and runs the selected
version for the current project without relinking the host's global `php` command.

`jvm` is the Java engine adapter. It declares `supports = "engine,jvm"`, finds JDKs
from `JAVA_HOME`, SDKMAN, `~/.jdks`, asdf, Homebrew and the system locations
(user-level installs before system ones), and selects one by major version
(`ctx set jvm 21`). It runs `java`, `javac` or `jar` from the selected
installation with `JAVA_HOME` set, without changing the host's default Java.

`wasm` is the built-in WebAssembly engine. It is self-contained: modules run in the
embedded wazero runtime (WASI Preview 1), so it needs no system runtime and works
on every platform ext builds for, including headless machines. `ctx run wasm
module.wasm args...` needs no selection. The module is sandboxed: it sees no host
environment, filesystem or network unless a flag grants one (`--env K=V`,
`--dir GUEST=HOST`, `--ro-dir GUEST=HOST`), and `--timeout` and `--memory-pages`
bound it. Standard streams belong to the command line and are connected.

The engine is chosen by what the module needs, not by what the machine has. A WASI
or pure module runs here, always. A web module (one that imports JavaScript glue
such as wasm-bindgen's `wbg`, Go's `gojs` or Emscripten's web runtime) cannot, and
`ctx run wasm` stops with exit status 126 and says so, instead of failing on the
first missing import. A webview engine for such modules is not installed or built
yet. `--inspect` reports what a module imports and whether it can run, without
running it.

`evm` is the built-in EVM engine. It executes EVM bytecode in memory with no
blockchain, node or network, so it is deterministic and sandboxed. The selection is
the rule set: `ctx set evm cancun` (`paris`, `shanghai`, `cancun` and `prague` are
offered; the default is `prague`). `ctx run evm [flags] BYTECODE|@file` prints one
JSON result with the status (`success`, `revert`, `error` or `timeout`), gas used
and return data, and exits non-zero unless it succeeded. `--input` supplies
calldata, `--deploy` treats the code as creation code and then calls the new
contract in the same run, and `--gas`, `--timeout`, `--value`, `--number`,
`--timestamp`, `--chain-id`, `--origin` and `--coinbase` set the limits and block
context. The block context is fixed by default so runs are repeatable. State lives
only for one run. The executable links go-ethereum, which is LGPL-3.0; see the
`NOTICE.md` shipped with the adapter. Write flags directly after `evm`, without a
separate `--`.

`git` is an optional computer-runtime adapter for Git commands and repository
hooks. It adds no Git shim. Its hook subcommand writes marked blocks to shell
hooks without changing `core.hooksPath`; an explicit directory can target an
editable hook source managed by another tool. See [Git hooks](../docs/git-hooks.md).

Shell-launched AI CLIs use the `computer` runtime and declare
`computer_commands` and optional `computer_capabilities` in their manifest.
Computer integrations can install command shims and provide native hook and
plugin operations. ctx manages the small hook entries that call its bridge;
handlers retain each provider's payload and response contract, and plugin
commands remain provider-specific. See [the adapter API](../docs/adapter-api.md#computer-side-cli-integrations).
The maintained `claude_code` and `codex` packages are available in the optional
installer catalog. Their shims route launches through ctx, hooks forward JSON stdin to
the project-resolved `computer_hook_command` (or the fallback environment
variable `CTX_COMPUTER_HOOK_COMMAND`) with the event name, and plugin operations
delegate to each CLI's native plugin subcommand. `ctx computer hooks install`
writes the CLI's project settings and records the selected handler and events
in the local `.ctx` file.
