# ctx adapter API v2.0

An external adapter is a directory containing `adapter.toml` and an executable
for the current operating system. It can be distributed as a platform-specific
`.ctxadapter` archive or through a platform index.
ctx installs adapters under `$EXT_HOME/adapters`; it never discovers or sources
code from the current directory or arbitrary `PATH` entries.

The home is shared by every ext product, so an adapter installed and trusted
once is available to ctx and ctn alike; trust is global to the user, not per
product. `EXT_HOME` sets the home and `EXT_ADAPTER_HOME` sets just the adapter
directory. `CTX_HOME` and `CTX_ADAPTER_HOME` are still honored for existing
installations, and an existing `~/.config/ctx` directory keeps being used until
`~/.config/ext` is created. A product that does not understand an adapter's
runtime ignores it.

Start with [Build a CTX adapter](adapter-authoring.md) for required files,
operation handlers, source builds, packaging, and installation. This page is
the detailed API reference.

## Architecture and ownership

Anything specific to a product, provider, browser, or native tool belongs in its
adapter package. The adapter owns native protocols, installation and activation,
discovery conventions, storage formats, signing tools, store URLs, settings
paths, and diagnostics. CTX core owns generic contracts, validation, trust,
selection, and dispatch through declared capabilities.

Shared libraries provide portable workflows and utilities without choosing
native behavior by adapter name or supplying a product-specific fallback.
Related adapters may import an engine from the adapter that owns a common native
mechanism. Product paths, identities, and supported routes are supplied by each
product's adapter configuration. Adding an adapter should not require a new
provider switch in core code.

## Manifest

~~~toml
api_version = "2.0"
name = "example"
display_name = "Example App"
runtime = "computer"
surfaces = "shell,web"
executable = "ctx-example"
# Optional native Windows implementation. When absent, executable is used.
executable_windows = "ctx-example.ps1"
description = "Example context adapter"
capabilities = "list,observe,validate,run,doctor,open,share"
supports = "virtualizer,container"
selector_key = "example_context"
extra_keys = "example_namespace"
commands = "example,examplectl"
# Optional when capabilities includes share; defaults to adapter name.
share_spaces = "workspace,project"
# A share-only store can opt out of project context selection.
# selectable = "false"
# Optional platform-specific runtime dependency: share space, adapter, API.
# dependencies_darwin = "credential:keychain@2.0"
# Native variables that take priority over the stored selection.
override_env = "EXAMPLE_CONTEXT,EXAMPLE_HOST"
# Optional for the manager runtime. At most one installed provider should set it.
default_provider = "false"
~~~

Names use lowercase letters, numbers, and underscores, and cannot collide with a
runtime, surface, or ctx command.

`display_name` is optional, user-facing adapter metadata (up to 80 Unicode
characters, without control characters). CTX falls back to `name` when it is
absent. It appears in `ctx adapter inspect` and CTX's credential-request notice
when this trusted adapter declares the selected credential-store dependency.
The label is not an authentication identity and does not change native OS
permission dialogs. An adapter changing its manifest needs to be trusted again.

`validate` and `doctor` are required. An adapter must provide `run`, `open`, or
`share`; `list` is optional. A share-only browser adapter can expose profile discovery and browser resources
without implementing URL launching.
`selectable = "false"` omits the selector key and command shim for a share-only
adapter. It defaults to `true` for existing adapters.

`dependencies_darwin`, `dependencies_linux`, and `dependencies_windows` are
comma-separated `space:adapter@api` declarations. The API version must match
the dependency's adapter API exactly. Catalog installation installs and trusts
missing dependencies; an already installed dependency must satisfy the declared
share space and remain trusted. Manual package installation does not silently
add dependencies. CTX passes a declared dependency to the child process as
`CTX_DEPENDENCY_<UPPERCASE_SPACE>`; operations that use it fail if the store is
missing or untrusted, without blocking unrelated adapter operations.
`ctx adapter remove` refuses to remove a dependency while an installed adapter
declares it; `--force` is available when intentionally breaking that feature.

### Credential stores

A credential store adapter declares `validate,doctor,share`,
`share_spaces = "credential"`, `selectable = "false"`, and
`self_contained = "true"`. Multiple installed adapters can register this
reserved share space; `ctx credential` selects one by the `adapter:item`
reference. CTX invokes `share "" -- credential get <item>` to receive up to
1 MiB of secret bytes on stdout, or `share "" -- credential put <item>
[--replace]` with the bytes on stdin. The adapter must reject overwrites unless
`--replace` is present and must not print the value in diagnostics. Its item
syntax and native lookup behavior are adapter-owned. See
[native credentials](credentials.md) for the CLI and security contract.
Browser adapters can use `res/credential/client.GetDeclared` to request an item
through CTX rather than importing a store implementation package. The host
checks the installed adapter's trust and API before dispatching.

### Machine graph observation

An adapter can declare the optional `observe` capability. CTX calls its
executable as `observe` with no selection or arguments and expects one UTF-8
JSON object on stdout:

~~~json
{
  "version": 1,
  "contexts": [
    {"selection": "work", "capabilities": ["validate", "run", "share"], "supports": ["container"], "attributes": {"label": "Work"}}
  ],
  "resources": [
    {"id": "project-1", "kind": "project", "context": "work", "attributes": {"label": "Example"}}
  ],
  "relations": []
}
~~~

`selection` is the value passed to the adapter's native operations. Context
capabilities and supports, when supplied, must be subsets of the manifest and
narrow what the context offers. Resource IDs are local to the adapter and
stored as graph digests. Relations use resource IDs as `from` and `to` and a
plain `kind`.
Browser contexts may also report `browser_share` as a map from a declared
resource operation to `ready`, `blocked`, or `unknown`.
Contexts and resources may have ordinary JSON metadata; never include cookies,
keys, tokens, or other credentials. Unknown fields, duplicate IDs, invalid
relations, or a response over 1 MiB invalidate the observation. CTX limits
discovery to 10 seconds. A failing collector removes its prior contexts on the
next successful graph scan. `observe` works for any runtime; no provider name
is built into the graph collector. Adapters without `observe` retain the
line-oriented `list` behavior documented below.

The public `github.com/webong/ext/pkg/graph/system` package exposes
`ObserveInventory`, `ResolveInventory`, and `Inventory.Find` for other Go
consumers. `ctx graph resolve [runtime|all] [capability...] [--supports <kind>] [--share <resource.operation>]`
shows the same candidate view as JSON. `ctx graph vertices support` and
`ctx graph edges supports-kind` expose declarations even for adapters that
have not listed a context. These are observations; the caller must validate
the selected context with its trusted adapter before acting.

`runtime` identifies the adapter's role, not whether it uses virtualization:

- `computer` for host-native CLIs and applications, including cloud,
  orchestration, database, and AI tools;
- `manager` for providers that manage workload engines or contexts, including
  Docker, Podman, nerdctl/containerd, and Apple Container;
- `browser` for browser-profile providers.

`surfaces` identifies how users interact with the adapter. `shell` permits
`run` and command shims; `web` permits `open`. An adapter may expose both.
`capabilities` names executable operations such as `run`, `build`, and
`image_save`. `supports` names resource kinds that the native tool can manage,
such as `virtualizer`, `container`, and `interpreter`. A tool may declare both regardless of
its runtime. Support declarations aid discovery; they do not add transfer or
invocation operations. API v2.0 does not accept `kind`.

An **engine** is a runtime that executes programs, such as a Java virtual machine,
a WebAssembly runtime, or an EVM node. An adapter that provides one declares
`supports = "engine,<kind>"`, for example `engine,jvm`, so
`ctx graph resolve all --supports engine` lists every engine and
`--supports jvm` narrows to one kind. The adapter's `list` output names the
installed engines (a major version for the JVM), `observe` reports their
version, location and source as attributes, and `run` executes the requested
command with the selected engine. Discovery conventions such as `JAVA_HOME`
belong to the adapter, not to the host.

A browser adapter normally uses `selector_key = "browser"`. Its `list` output
uses `name:profile` values, while `validate`, `doctor`, and `open` receive only
the profile portion as their selection. `ctx set name:profile` uses the
adapter's declared `browser` runtime to store that value under the `browser`
selection key.

A browser adapter participating in `ctx share:browser` declares, for example:

~~~toml
runtime = "browser"
capabilities = "list,validate,open,doctor,share"
share_spaces = "browser"
browser_share = "cookie.list,cookie.export,cookie.query,cookie.import,policy.export,certificate.list,certificate.export,certificate.import"
# Optional automatic query order (lower is earlier; default 1000).
browser_query_priority = "80"
# Optional. False requires callers to name this adapter explicitly.
browser_query_auto = "true"
~~~

Browser adapters can also expose profile-management workflows with the
optional `browser_management` field. It requires the browser runtime and the
`share` capability. For example:

~~~toml
browser_management = "extension.targets,extension.prepare,extension.package,extension.stage,extension.install,extension.activate,userscript.prepare,userscript.install,userscript.activate,bookmarklet.encode,bookmarklet.decode"
~~~

CTX invokes these through `manage <kind> <action>` with one versioned JSON
request on stdin. See [Browser management](browser-management.md) for the
public Go API, operation list, and lifecycle boundaries.

A manager adapter's unqualified `list` output contains native context,
connection, or namespace names. `ctx ls manager` prefixes each result as
`name:selection`. A provider may implement `build`, image, and volume
capabilities in addition to normal `run` routing. `ctx share:manager image
copy` and `ctx share:manager volume copy` resolve qualified endpoints
dynamically, so additional providers need no core changes.

Users may register named manager instances with `ctx manager add`. Each
instance records an adapter provider and native selection, plus optional
declared virtualizer product and machine labels. Share and build commands accept `@instance`
endpoints. The instance registry is stored in `$EXT_HOME/managers.json` (or
the default ctx configuration home), with owner-only permissions. Registration
validates the native selection through the trusted adapter unless the user
explicitly passes `--offline` to register an unavailable engine. ctx displays the
resolved instance before a transfer. The product and machine are user-declared
labels; the adapter remains responsible for routing to the selected engine.
For nerdctl, registration can include `--address`, which ctx passes as
`CTX_MANAGER_ADDRESS` to adapter invocations. The maintained nerdctl
adapter passes this value as its native `--address` flag for validation,
transfer, and build operations. Other manager adapters need no new
capability: their native selection already identifies the engine connection.
Instances may also pin `--command` to an absolute executable. Docker instances
can provide `--plugin-dir` and repeat `--plugin buildx=/absolute/path` or
`--plugin compose=/absolute/path`. ctx passes the selected CLI as
`CTX_ADAPTER_REAL_COMMAND` and gives that invocation a temporary `DOCKER_CONFIG`
with isolated plugins. Existing contexts and credentials remain available;
global Docker plugin links are not rewritten. The core resolves `@instance`
selectors before calling the adapter, so adapters continue to receive their
native selection. `ctx manager doctor` checks toolchain paths and host-side
manager issues without changing any configuration.

A computer adapter may also set `self_contained = "true"` while declaring
`computer_commands`: it is then its own implementation, and ctx does not look for
a native executable of that name. The built-in `wasm` engine works this way.

An API v2.0 manager adapter may set `self_contained = "true"` and omit
`commands` when its executable handles its own native CLI discovery. Without
this opt-in, `commands` still defaults to the adapter name. Manager-app adapters declare
`supports = "virtualizer"` and implement `list`, `validate`, `run`, and
`doctor`; `ctx manager app <adapter> <status|start|stop|doctor>` routes these
operations generically. The maintained Rancher Desktop, OrbStack, and Docker
Desktop packages own their product-specific status and diagnostic logic. The
core does not decide which desktop app owns Docker CLI plugins.

`selector_key` defaults to the adapter name, or to `browser` for browser
adapters. Direct computer integrations that only declare `computer_commands`
or `computer_capabilities` need no selector. `extra_keys` declares additional
profile values owned by the adapter. `commands` defaults to the adapter name
for selectable non-browser adapters and lets `ctx run` route native command
names to the adapter. `override_env` declares native environment variables, in
precedence order, that `ctx status` should report instead of the stored
selection. The provider remains responsible for honoring them during `run`.
`default_provider` lets unqualified build, image-sync, and volume endpoints
choose a manager provider without hard-coding an engine in the core;
multiple trusted defaults are reported as an error.

ctx still loads API `1` and `1.0` manifests. Their legacy `kind` is translated
at load time: `selector` becomes the `computer` runtime on the `shell` surface,
`browser` becomes `browser` on `web`, and `container` becomes `manager` on
`shell`. New and updated adapters should declare `runtime = "manager"`.

## Computer-side CLI integrations

Computer integrations declare their host commands and capabilities directly.
For example, a Claude package can declare:

~~~toml
api_version = "2.0"
name = "claude_code"
runtime = "computer"
surfaces = "shell"
computer_commands = "claude"
computer_capabilities = "hook,plugin"
capabilities = "validate,doctor,run"
extra_keys = "computer_hook_command"
computer_hook_events = "PreToolUse,PermissionRequest,PostToolUse"
computer_hook_default_events = "PreToolUse,PermissionRequest"
computer_hook_settings_file = ".claude/settings.local.json"
computer_hook_template = "computer-hooks.json"
~~~

`computer-hooks.json` contains the adapter's native hook object with
`{{event}}` and `{{adapter}}` placeholders. A different computer adapter can
use a different path, event set, and template format while using the same ctx
commands.

The package includes a `claude` launcher file that delegates to
`ctx run claude "$@"`. When trusted, ctx installs that launcher beside `ctx`.
It locates the real CLI with the shim directory excluded from `PATH` and passes
its absolute path in `CTX_ADAPTER_REAL_COMMAND`, preventing recursive shim
calls. Computer commands do not require a project selection; the adapter still
receives the active project and profile environment.
For a Codex integration, declare `computer_commands = "codex"` and include the
matching `codex` launcher in its package.

`computer_capabilities` currently accepts `hook` and `plugin`.
Adapters that support project hook setup also declare their own
`computer_hook_events`, optional `computer_hook_default_events`,
`computer_hook_settings_file`, and `computer_hook_template`. The template is a
JSON object rendered once per selected event; it may use `{{event}}` and
`{{adapter}}`. The adapter package owns the native settings path, schema, event
names, and generated entry shape. ctx only validates declared events and
generically merges or removes the rendered JSON patch, preserving unrelated
project settings. No provider names or native file formats are built into
`src/ctx/internal/app`.

`ctx hook computer <adapter> <event>` passes the native hook payload from stdin
to the adapter as `hook -- EVENT`; stdout, stderr, and exit status flow back to
the CLI runtime. The adapter owns the event schema and response contract.
`ctx plugin computer <adapter> [ARGUMENTS...]` invokes
`plugin -- ARGUMENTS...` for provider-specific extension setup. These operations
use the same adapter trust and checksum checks as shims. `ctx computer hooks
print|install|remove <adapter>` previews or manages project hooks in the
native settings file declared by that adapter. Install preserves unrelated
settings and records `computer_hook_command`,
`computer_<adapter>_hooks`, and the exact generated patch in the current
directory's `.ctx` file. The handler path and event list therefore resolve per
project when a CLI hook invokes ctx from that directory. Plugin operations
remain adapter-owned CLI passthroughs, so native project-scope flags and
configuration are decided by the selected adapter's CLI.

The optional `claude_code` and `codex` packages install `claude` and `codex`
shims. Put ctx's bin directory before the vendor CLI directory on `PATH`, then
activate them with `ctx setup --adapters claude_code,codex`. `ctx run claude
--version` and `ctx run codex --version` reach the installed CLIs. To install
project hooks, provide a handler executable; ctx passes the event name as its
first argument and preserves the vendor's JSON stdin/stdout, stderr, and exit
status:

~~~sh
ctx computer hooks install claude_code --handler ./scripts/ctx-policy
ctx computer hooks install codex --events PreToolUse,PermissionRequest --handler ./scripts/ctx-policy
ctx computer hooks print claude_code
ctx computer hooks remove claude_code
~~~

Claude Code hooks are written to `.claude/settings.local.json`; Codex hooks are
written to `.codex/hooks.json`. These are project-scoped native config files,
with the handler and event list also stored in the git-ignored `.ctx` file. The
installer preserves existing JSON keys and other hook entries. The event list
defaults to `PreToolUse,PermissionRequest`, and a later install without
`--events` reuses the project-local selection. For plugin operations, ctx
delegates to the native CLI and accepts its scope flags:

~~~sh
ctx plugin computer claude_code install <plugin>@<marketplace> --scope project
~~~

The plugin command is a pass-through. Claude Code's `--scope project` targets
the current repository. Codex project plugins use its repository marketplace
and `.codex/config.toml` settings; ctx does not scaffold those files yet.

This boundary matches Neura Relay's builder flow: an action is reviewed, a
decision receipt is returned, and the CLI runtime decides what to do next.
Local settings can independently control agent authority, outside actions, and
sensitive changes. See [Neura for Builders](https://www.neurarelay.com/builders)
and [Neura Local settings](https://www.neurarelay.com/operators#neura-local-settings).

## Process protocol

CTX adapters also expose a shared library descriptor through
`github.com/webong/ext/pkg/plugin.AdapterDescriptor`, using the `ext.adapter`
contract and their native API version. CTX uses the public `plugin` package
for capability lookup and package integrity checking. The argv and stream
binding below remains the adapter transport; it does not require a JSON-line
handshake. See [the shared plugin contract](adr-plugin-contract.md) for the
public host API and Xallet/Cymonkey integration boundary.

ctx selects `executable_windows` on Windows when it is present and otherwise uses
`executable`. Windows adapters may be `.exe`, `.cmd`, `.bat`, or `.ps1`; PowerShell
scripts are launched without loading the user's profile.

ctx invokes the selected executable with one of these forms:

~~~text
ctx-example list
ctx-example validate SELECTION
ctx-example configure SELECTION -- OPTIONS...
ctx-example doctor SELECTION
ctx-example run SELECTION -- ARGUMENTS...
ctx-example open SELECTION -- ARGUMENTS...
ctx-example share SELECTION -- SPACE ARGUMENTS...
ctx-example hook SELECTION -- EVENT
ctx-example plugin SELECTION -- ARGUMENTS...
~~~

Computer endpoint operations omit the selection argument when none applies, so
their forms are `run -- ARGUMENTS...`, `hook -- EVENT`, and
`plugin -- ARGUMENTS...`.

Every declared capability is invoked by the same protocol:

~~~text
ctx-example CAPABILITY SELECTION -- ARGUMENTS...
~~~

Manager capabilities currently understood by the core are `build`,
`image_push`, `image_pull`, `image_save`, `image_load`, `volume_exists`,
`volume_create`, `volume_export`, and `volume_import`. Image save arguments are
`ARCHIVE IMAGE...`; image load receives `ARCHIVE`. Volume export writes a tar
stream to stdout, while volume import reads a tar stream from stdin.

Manager resource transfers are exposed through `ctx share:manager`.
The installed manager adapters register the actual image and volume
capabilities, and ctx rejects a transfer when either endpoint lacks a needed
capability. `image sync` uses registry push/pull by default and falls back to
an archive when an endpoint lacks push/pull. `image copy` and `image sync
--tar` use an archive. Apple Container supports the
archive path. Volume export and copy are point-in-time operations; ctx refuses
to import into an existing target volume. `ctx build --cache-ref` uses a
registry-backed build cache where the chosen adapter supports `build`.
`ctx share:browser` bridges browser resources between trusted
adapters. Browser adapters declare `share` and `share_spaces = "browser"`, then
list operations in `browser_share`, for example
`cookie.list,cookie.export,cookie.query,cookie.normalize,cookie.import,policy.export`. ctx invokes an
operation as `share PROFILE -- RESOURCE OPERATION`. The adapter receives one
JSON request on stdin with `version = 2`. `cookie.list` receives `site` and
returns a JSON array of cookie metadata without values. `cookie.export`
receives `site` and a listed `cookie`, then returns that cookie with its value.
`cookie.import` receives `bundle` and `replace`, and returns no body.
`cookie.query` receives `site` and optional `names`, `include_expired`, and
`allow_all_hosts`. The site can be omitted only when `allow_all_hosts` is true.
It returns `{"cookies":[...],"warnings":[...],"store_path":"..."}`; cookies include their values,
while warnings describe individual reads that failed. Do not put cookie values
in warnings. `store_path` is optional and identifies the original on-disk
cookie store when one path represents the query. Go adapters using `CookieBackend`
set `QueryHandleIsStorePath` only when their query handle is that store path;
otherwise the handle stays opaque and no path is inferred. Query results must obey the requested site, name, and expiry
filters. An adapter that cannot query all hosts or include expired rows should
omit `cookie.query` or reject those options explicitly.
`cookie.normalize` receives `store_id` and opaque `native_export` in the
version-2 request. The owning adapter validates the export's browser/profile/store
binding and native scope, returning `{"cookies":[...],"store_id":"..."}` with
canonical cookies. The store must match the request. The generic host requires
an explicit endpoint and trusted adapter; it does not interpret native fields.
Go adapters supply `CookieBackend.Normalize(profile, storeID, payload)`.
Maintained browser-API envelope rules and scope-preservation limits are documented
in [cookie normalization](browser-cookies.md#normalize-an-authorized-browser-export).
Chromium-family Go adapters can use the owning engine's
`chromium.NewCookieBackend(config)` to retain keys and credential lookup
failures for one request. Construct a fresh backend for each request; the
backend is not concurrency safe and credentials must not be cached globally.
Host-side query output supports JSON, a single-site HTTP Cookie header, and
Netscape jars; text formats reject partitioned or adapter-scoped records.
See [cookie workflows and validation](browser-cookies.md).
`policy.export` receives only `version` and returns a policy bundle with
`version` and `entries`. A nonzero exit code reports failure on stderr.
Cookie `id` is a source row ID where available; adapters without row IDs can
return an opaque `ref`. Adapters must preserve cookie scope and reject fields
they cannot map. ctx selects one listed cookie before exporting its value,
matching the request URL's scheme and host, and its path when supplied, before either operation,
then delivers a versioned bundle to a file, pipe, or importing adapter. A
source and target can be different browser providers. The maintained browser
adapters package their own `ctx-<adapter>-share` executable, built from that
adapter's `native` directory; it is included in the adapter checksum and runs
as a separate process. External adapters implement the same protocol in their own
executable. The helper receives the same invocation as every adapter,
`share <profile> -- <resource> <operation>` (or `-- management <kind> <action>`),
and parses it with `plugin.ParseAdapterInvocation` from `pkg/plugin`; the
adapter's shell or PowerShell entry point passes its argv through unchanged.
CTX core has no browser-specific storage code or provider dispatch.
Maintained adapters use `res/browser/contract` for versioned request, cookie,
policy, and generic resource envelope types. External adapters implement the
documented JSON contract directly. Cookie fields
shared across browsers are portable; optional browser-specific fields go in
`attributes` with namespaced keys such as `firefox.origin_attributes`.
CTX treats these attributes as opaque and compares the complete map when
merging cookies, so adapters can preserve their native cookie scopes.
Firefox adapters may also expose `firefox.container_id` and
`firefox.container_name` for explicit cookie selection; container IDs are
profile-local and must not be silently copied to another profile.
Importers must reject attributes they cannot preserve. Version 2 replaces the
earlier version 1 browser share request and bundle format.

An adapter may implement `share PROFILE -- status probe` to report local
prerequisites without reading cookie values or requesting OS credentials. It
reads no stdin and returns one JSON object, for example
`{"version":1,"operations":{"cookie.list":"ready","cookie.export":"unknown"}}`.
Keys must be declared `browser_share` operations. States are `ready` (local
prerequisites confirmed), `blocked` (a known prerequisite is missing), or
`unknown` (an attempt is needed to decide). The probe must not emit secrets or
profile paths. Absent and invalid probes become `unknown`; statuses are
observations, and operations still validate live state. A graph scan probes up
to 32 profiles per adapter. Statuses appear in `ctx graph resolve browser` and
`ctx share:browser capabilities --from name:profile`; `ctx graph resolve
browser --share cookie.list` selects profiles reporting `ready`. Adapters using
`observe` may provide the same `browser_share` map per context in their
observation JSON.

`res/browser/guest` contains the portable implementation for serving the
browser protocol. Native policy sources, executable discovery, and SQLite
tool execution are owned under `adapters/`.
External Go adapters import the shared types from
`github.com/webong/ext/res/browser/contract` and serving helpers from
`github.com/webong/ext/res/browser/guest`. A Go browser adapter can handle
`share <profile> -- <resource> <operation>` in its main executable, parse it with
`plugin.ParseAdapterInvocation`, and use `guest.RunCookie`; it does not need a separate
helper executable. The bare Chromium adapter owns the reusable native engine at
`github.com/webong/ext/adapters/chromium/engine`; Chrome and other
Chromium-based Go adapters can configure and import it through its exported
`Config`, `Cookie`, `List`, `Query`, `ReadValue`, `Import`, `Probe`, and
`RunManagement` API. The same engine owns CDP installation and activation, CRX
packing, configured external store registration, and userscript session execution.
Chrome and Edge supply their own executable paths, store URLs, and registration
locations. The Firefox engine owns WebDriver BiDi installation and activation,
profile discovery, and Mozilla signing. Safari owns app inspection, building,
and its native installation handoff. The versioned JSON
contract remains the interface for external adapters.

Other Go services can import `github.com/webong/ext/res/browser` and call
`browser.Get` with a `browser.Backend` supplied by their host. The backend
selects authorized sources and invokes them; CTX's host backend also checks
installed-adapter trust. The resource combines scoped cookies and returns
source warnings. Its options
also support ordered merge/first results, browser/profile discovery, inline
JSON/Base64/file cookies, fallback inline cookies after adapter reads, timeout,
all-host reads, and expired cookies. A
trusted adapter with only list/export can serve ordinary site queries; the
all-host and expired modes require `cookie.query`.
`Options.PreferredSource` moves one `browser:profile` endpoint ahead of
automatic discovery. The CLI fills it from the selected ctx browser. During
automatic discovery, ctx sorts trusted adapters by `browser_query_priority`
and name, and omits those with `browser_query_auto = "false"`. Explicit
`Sources` and `Browsers` always retain caller order and can select an omitted
adapter. `PreferredSource` is an explicit selection and can also select an
adapter omitted from automatic discovery. Result cookies retain a `source` label and add structured
`source_info` with adapter, profile, and any adapter-reported store path.
Inline fallback cookies carry `source_info.fallback = true`.
The library also exposes `browser.ParseCookies` for portable JSON and Netscape
exports, and `browser.BundleCookie` for a selected single-cookie import. Cookie
input validation preserves native scope and rejects fields it cannot interpret.
Queries retain nonfatal source/row warnings; cancellation and timeout return an
error. The CLI's `--strict` and `--require-match` flags withhold output on warnings
or empty results respectively. See [cookie inputs](browser-cookies.md).

For example, `cookie.list` receives `{"version":2,"site":"https://example.com"}`
and can respond with:

~~~json
[{"ref":"profile-cookie-42","name":"session","domain":"example.com","path":"/","expiry":1893456000,"secure":true,"http_only":true,"same_site_policy":"lax"}]
~~~

The `ref` must select the same cookie during `cookie.export`; the export
response adds `value`. `cookie.import` receives the exported cookie inside a
`bundle` object with `version`, `source`, and `site`, plus `replace`. Adapters
should not write secret values to stderr or include them in `cookie.list`.

Other browser resources use the generic bridge without a ctx code change.
An adapter declares `resource.list`, `resource.export`, and/or
`resource.import` in `browser_share`. The user runs
`ctx share:browser <resource> <list|export|copy|import>`. The adapter receives
`{"version":2,"args":[...]}` for list and export; arguments after `--` are
passed through in `args`. Export returns an envelope containing `version:2`,
`resource`, and a JSON `payload`. ctx sets `source` to the selected provider and
profile. Copy and import deliver that envelope as `bundle` with a `replace`
boolean. The destination adapter validates the payload's resource-specific
schema and determines whether it can represent the source data. Generic
export supports `--to-file` and `--stdout`; generic import supports
`--from-file` and `--stdin`. Files are created with mode 0600, and stdout
requires a pipe. The maintained Firefox adapter implements
`certificate.list`, `certificate.export`, and `certificate.import` with a
password-protected PKCS#12 payload. The generic bridge passes adapter
arguments to both ends of a copy, so a local password file can be used for
export and import.
`ctx share:computer` is also reserved. Any other installed adapter can declare
the `share` capability and optional `share_spaces` to register
`ctx share:<space>` commands. When `share_spaces` is omitted, the adapter name
is the space. CTX rejects ambiguous registrations, then invokes the trusted
adapter's `share` operation with the resolved selection. The first argument
after `--` is the space, followed by the user's remaining arguments; the
adapter owns their meaning.

The process receives `CTX_ADAPTER_API`, `CTX_ADAPTER_NAME`,
`CTX_ADAPTER_COMMAND`, `CTX_ADAPTER_REAL_COMMAND`, `CTX_PROJECT_DIR`, and
`CTX_PROFILE`. For manager providers, `CTX_ADAPTER_REAL_COMMAND` is the
resolved underlying CLI path with ctx's transparent shim excluded. Values for declared
keys are exported as `CTX_ADAPTER_VALUE_<UPPERCASE_KEY>`. A `configure` operation
prints tab-separated `key<TAB>value` records for declared keys; ctx validates and
writes them to `.ctx`. Selections are identifiers, never credentials. Exit status 0
means success, 1 means validation or runtime failure, 2 means adapter usage error,
and 127 should identify a missing dependency. Normal data belongs on stdout;
diagnostics belong on stderr.

Adapters are separate processes and must not expect their environment changes to
affect ctx or its parent shell. They should use the selected native profile or
context when launching their underlying tool.

## Lifecycle

~~~sh
ctx adapter available
ctx adapter add example
ctx adapter refresh
ctx adapter test ./example
ctx adapter install ./example
ctx adapter inspect example
ctx adapter trust example
ctx adapter doctor example
ctx adapter remove example
~~~

### Binary packages and Go builds

`ctx adapter install` accepts a ready-to-run directory, a local `.ctxadapter`
archive, or an HTTPS archive with `--sha256 <digest>`. Installation never runs
Go or executes the adapter. The archive has `adapter.toml`, the platform's
executable, optional runtime assets, and a `ctx-package.json` record containing
`format_version`, `name`, `os`, and `arch`. ctx rejects archives for another
platform, unsafe paths, links, oversized contents, and checksum mismatches.
It installs the package untrusted; review it and run `ctx adapter trust <name>`
before using it.

Go authors can use the public `github.com/webong/ext/pkg/plugin` package to parse
the versioned process invocation. Browser authors can also import
`github.com/webong/ext/res/browser/contract` for the cookie and policy bridge
types, and `github.com/webong/ext/res/browser/guest` for serving helpers, while
keeping their own browser-specific storage code. A source
directory needs `adapter.toml` and
a Go `main` package. `go_entry` names that package (default `.`), and optional
`package_files` lists runtime files or directories to ship. Source code and
`go.mod` are not copied into the archive unless deliberately listed.

~~~toml
api_version = "2.0"
name = "example"
runtime = "computer"
surfaces = "shell"
executable = "ctx-example"
executable_windows = "ctx-example.exe"
capabilities = "list,validate,run,doctor"
go_entry = "./cmd/ctx-example"
package_files = "policy.json,templates"
~~~

~~~sh
ctx adapter build ./example
ctx adapter build ./example --os linux --arch amd64
ctx adapter pack ./ready-package --os linux --arch amd64 --output ./example-linux-amd64.ctxadapter
ctx adapter install ./example/dist/example-darwin-arm64.ctxadapter
ctx adapter trust example
~~~

`ctx adapter build` requires Go and emits one archive per target. Cross builds
default to `CGO_ENABLED=0` unless the author sets it explicitly. The example
under `examples/adapters/go_echo` shows the public parser and a complete Go
adapter. An adapter written in another language can prepare a package directory
and use `ctx adapter pack` without Go. `pack` includes every regular file in the
given directory, so use a directory containing only runtime files.

Publishers may provide a `.ctxadapter.json` index so users need one install
reference across platforms. Each package entry includes its own SHA-256 digest;
an HTTPS index itself also requires `--sha256` on install. Relative package URLs
are resolved against the index URL. Place the platform archives in one directory
and run `ctx adapter index ./dist/example.ctxadapter.json ./dist/example-*.ctxadapter`
to generate the index and its package checksums. The command prints the index
SHA-256 to give installers out of band.

~~~json
{
  "format_version": 1,
  "name": "example",
  "packages": {
    "darwin/arm64": {"url": "example-darwin-arm64.ctxadapter", "sha256": "<archive-sha256>"},
    "linux/amd64": {"url": "example-linux-amd64.ctxadapter", "sha256": "<archive-sha256>"}
  }
}
~~~

~~~sh
ctx adapter install https://example.com/example.ctxadapter.json --sha256 "$INDEX_SHA256"
ctx adapter trust example
~~~

The optional installer-provided catalog lives under `$EXT_HOME/catalog/adapters`
(or `CTX_CATALOG_HOME`). A default core-only installation has no catalog.
Request adapter selection with the installer to obtain it; `ctx setup` and
`ctx adapter add` then copy selected catalog packages into the active adapter
store and trust their checksums. Catalog membership is installer metadata, not
a privilege or manifest property.

Direct `ctx adapter install ./directory` installation does not imply trust. Trust
records a checksum over every file in the adapter directory. Any subsequent
change makes the adapter untrusted until the user reviews and trusts it again.

### Native browser page backends

Adapters implementing the shared local manager can supply `PageSessionBackend`
with `PageRuntime(ctx, profile, input)`. It returns the native
`PageSessionRuntime`; endpoint discovery and connection behavior stay in the
adapter. CTX handles the generic `session.targets|connect|navigate|inject|replay`
workflow and userscript store reconciliation. `PageSessionState` exposes an
optional terminal channel and error for long-running consumers. Native engines
keep that channel open during automatic transport recovery, bounded to 30 seconds.
Recovery keeps the selected page ID and restores userscripts and document-start
injections; native adapters own the protocol and preload cleanup.

The Chromium engine exports `NewPageSessionRuntime(config, endpoint)` for CDP,
and the Firefox engine exports the corresponding BiDi factory. Chrome and Edge
supply product conventions through their own configs. The shared WebSocket
command transport lives under the Chromium adapter engine and contains no
product discovery or page behavior. Native reconnect coordination is also under
that adapter engine; each native backend supplies its own attachment and
ownership recovery. Firefox accepts explicit provider-owned session URLs for
resumable attachments and preserves those sessions on close. Core executables do not import either
native engine.

Native extension operation names include `extension.convert` (Safari project
conversion), `extension.policy` (adapter-owned force/unforce/block/unblock
policy routes), and `extension.update_manifest` (publishing metadata generation).
An adapter must declare each route it implements. See
[browser management](browser-management.md) for input schemas, custom profile
selection, lifecycle, and platform limits.
The [extension distribution guide](extension-distribution.md) describes CRX
inspection, hosting metadata, browser/platform capabilities, and local/server
registration. Native formats remain inside adapter packages.
