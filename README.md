# ctx

Project-local contexts for the tools you already use. Choose a Docker engine,
Kubernetes cluster, cloud profile, browser profile, database connection, or
shell environment for one project without changing a machine-wide default.

```sh
cd my-project
ctx set docker orbstack
ctx set kube development --namespace payments
ctx set aws client-a
ctx set 'firefox:client-a'

docker ps                         # ctx shim uses the project's Docker context
ctx run kubectl get pods
ctx run aws sts get-caller-identity
ctx open http://localhost:3000     # opens the selected browser profile
```

Selections live in the project's `.ctx` file. ctx stores selectors and
non-secret profile values; credentials remain with the native tools.

## What ctx does

| Area | Available today |
| --- | --- |
| Project contexts | Select contexts per project, group them into profiles, inspect resolution, and apply profile environment values to a command or child shell. |
| Container engines | Route Docker, Podman, nerdctl/containerd, and Apple Container. Use named engine connections, registry-backed build caches, and point-in-time image or volume transfers. |
| Desktop managers | Inspect and explicitly start or stop Rancher Desktop, OrbStack, and Docker Desktop through separate app adapters. Rancher diagnostics distinguish VM, k3s, and Docker-plugin issues. |
| Computer tools | Route Kubernetes, AWS, gcloud, PostgreSQL, MySQL, and PHP. Claude Code and Codex adapters add CLI shims and project hooks. |
| Browsers | Open a selected profile; query and share supported cookies and other browser resources; prepare extensions and userscripts; and attach to supported live pages. Capabilities vary by browser and OS. |
| Extensibility | Install trusted adapters on demand. The core provides selection, validation, trust, dispatch, and a graph of discovered contexts and capabilities. |

The core does not ship with an active adapter or a bundled adapter catalog. An
adapter owns its product-specific behavior; ctx only invokes its declared
capabilities. See [Build a CTX adapter](docs/adapter-authoring.md) to build one.

## Install

The default installer installs only the ctx core. Run it from a source checkout
with Go 1.23 or newer:

```sh
git clone https://github.com/webong/ctx.git
cd ctx
./install.sh
export PATH="$HOME/.local/bin:$PATH"
```

Choose adapters explicitly when you need them:

```sh
./install.sh --adapters docker,kube,aws,firefox
# Or: ./install.sh --interactive
# Or: ./install.sh --all
```

On Windows, use PowerShell:

```powershell
git clone https://github.com/webong/ctx.git
Set-Location ctx
.\install.ps1 -Adapters docker,kube,aws,firefox
```

Omit `-Adapters` for a core-only install, or use `-Interactive` or
`-AllAdapters`. The installer places `ctx.exe` in
`$env:LOCALAPPDATA\Programs\ctx\bin` by default; add that directory to `PATH`.

When a release is published, the remote installers do not require Go:

```sh
curl -fsSL https://raw.githubusercontent.com/webong/ctx/main/install.sh | sh
curl -fsSL https://raw.githubusercontent.com/webong/ctx/main/install.sh |
  sh -s -- --adapters docker,kube,aws,firefox
```

For Windows, download and run
[install.ps1](https://github.com/webong/ctx/blob/main/install.ps1) with the same
`-Adapters` selection. Release downloads are checksum-verified. The adapter
catalog is a separate, optional download; a core-only update preserves
previously installed adapters. If no release is published, install from source.
See [cross-platform support](docs/cross-platform.md) for platform details.

## Everyday use

```sh
ctx ls docker                    # discover native contexts
ctx set docker orbstack          # write this project's selection
ctx status                       # show active selections
ctx explain                      # show values and where they came from
ctx doctor                       # check selected contexts and adapters

ctx profile set client-a docker orbstack
ctx profile set client-a browser firefox:client-a
ctx profile env client-a APP_ENV development
ctx profile use client-a
ctx run -- npm test              # apply profile environment to one command
ctx shell                        # or start a child shell with that environment
```

Installed Docker, Podman, and nerdctl adapters can place small shims before
the native commands on `PATH`, so ordinary `docker`, `podman`, and `nerdctl`
commands honor the project selection. `ctx real docker` shows the executable
behind a shim. For other tools, use `ctx run <tool> ...`. Explicit native CLI
flags and environment variables take precedence over ctx.

## Managers and sharing

Container-engine adapters and desktop-manager adapters have different jobs.
For example, `docker` routes Docker commands while `docker_desktop` inspects
the Docker Desktop application. Install only the ones you use:

```sh
ctx adapter add rancher_desktop orbstack docker_desktop
ctx manager apps
ctx manager app rancher_desktop doctor
ctx manager app orbstack status
```

`doctor` is read-only. `start` and `stop` are separate, explicit actions.
You can also name an engine connection for builds and transfers:

```sh
ctx manager add orb --virtualizer orbstack --provider docker --selection orbstack
ctx manager add desktop --virtualizer docker-desktop --provider docker --selection desktop-linux

ctx build @orb --cache-ref ghcr.io/acme/api:docker-cache -- --tag acme/api:dev .
ctx share:manager image copy @orb @desktop acme/api:dev
ctx share:manager volume copy @orb @desktop app-data app-data
```

Image copy uses an archive; image sync can use a registry when both adapters
support it. A volume copy is a point-in-time transfer, **not live sync**: stop
or quiesce a database first. ctx refuses to import over an existing target
volume. Named connections can also pin a CLI and Compose/Buildx plugin
directory, avoiding changes to global Docker plugin symlinks. A registration
describes an engine inside a VM; it does not copy VM disks or snapshots.

## Browsers

The `ctx open` command launches URLs in a selected profile of Firefox,
Chrome, Chromium, Edge, Brave, or Safari. Other maintained browser adapters
may support sharing without implementing `ctx open`. Discover what a particular
installation can do with `ctx adapter inspect <name>` and
`ctx share:browser capabilities --from <browser:profile>`.

Cookie queries can read supported profile stores or caller-provided exports,
select by site/name/scope, and return JSON, an HTTP Cookie header, or a
Netscape cookie jar where that format can preserve the cookie's scope:

```sh
ctx share:browser cookie query --from firefox:personal \
  --site https://example.com --name session --to-file ./cookies.json

ctx share:browser cookie query --browser firefox --browser chrome \
  --site https://example.com --mode first --stdout | consumer

ctx share:browser cookie normalize --from chrome:Default --store-id 0 \
  --from-file ./authorized-export.json --to-file ./normalized-cookies.json
```

Sharing also covers supported cookie import, browser policy export, and
exportable Firefox certificates. Browser encryption, OS permissions, and
profile state limit what can be read or transferred. A query never bypasses
Chrome App-Bound encryption or a browser's access controls. Cookie values are
sensitive; file output is private, and stdout requires a pipe. See
[browser cookies](docs/browser-cookies.md) for input formats, fallbacks,
normalization, and security limits.

Trusted browser adapters can prepare and install extensions through supported
native routes, manage userscripts, encode bookmarklets, and attach to a page
whose debugging endpoint is already available:

```sh
ctx browser manage extension prepare --target chrome:Default \
  --input '{"source":"/path/to/extension.zip"}'

ctx browser manage session targets --target chrome:Default
```

Preparing or registering an extension is not proof that a browser installed or
enabled it. A page attachment does not launch or close the user's browser.
Read [browser management](docs/browser-management.md) for operations and
[extension distribution](docs/extension-distribution.md) for publishing and
platform-specific installation rules.

## Native credentials

Install the store adapter for your OS: `keychain` on macOS, `secret_service` on
Linux, or `credman` on Windows (`.\install.ps1 -Adapters credman`). ctx can then
move an explicitly named secret
without putting its value in `.ctx` or command arguments:

```sh
./install.sh --adapters keychain   # macOS; choose secret_service on Linux
chmod 600 ./secret.txt
ctx credential put 'keychain:service=ctx&account=work' --from-file ./secret.txt
ctx credential get 'keychain:service=ctx&account=work' --stdout | consumer
ctx credential copy 'keychain:service=ctx&account=work' \
  'keychain:service=ctx&account=work-copy'
```

`get --stdout` requires a pipe; `get --to-file` creates a new private file.
`put` reads from a private file or redirected stdin. An existing item requires
`--replace`. Copy is a point-in-time transfer, not synchronization. Native
permissions and unlock prompts still apply. Chromium-family browser adapters
declare a runtime credential dependency on Keychain (macOS) or Secret Service
(Linux). Selecting one from the catalog installs its store adapter too; the
browser requests supported cookie keys through CTX's trusted adapter protocol.
Updating a store adapter does not require rebuilding the browser adapter. CTX
does not bypass browser-bound encryption. See [native credentials](docs/credentials.md)
for store reference formats, cross-machine transfer, and security limits.

## Adapters and the system graph

The optional installer catalog contains maintained adapters. After obtaining
it with `--adapters`, `--interactive`, or `--all`, you can add more locally:

```sh
ctx adapter available
ctx adapter add postgres mysql
ctx adapter ls
ctx adapter inspect postgres
ctx graph scan
ctx graph shells
ctx graph filesystems
ctx graph webviews
ctx graph processes
ctx graph process <pid>
ctx graph resolve shell --name zsh
ctx graph resolve filesystem --path ./export.json --writable --min-free 1048576
ctx graph resolve webview --engine webkit --api WKWebView
ctx graph resolve browser --share cookie.list
```

The graph discovers host shells, mounted filesystems, and shared webview
runtimes directly, even with no adapters installed. `graph shells` reports
executable paths, resolved symlink
targets, discovery sources, and the configured default. `graph filesystems`
reports mount points, filesystem types, sources, read-only status, and space in
bytes where available. `graph webviews` reports detected embedding runtimes and
their rendering engines, API/ABI generations, versions where available, and
discovery evidence. These commands emit JSON and refresh their graph records;
`graph processes` inventories running processes; `graph process <pid>` inspects
one process’s files, mappings, sockets, and usage where supported. Both report
collection coverage and work without adapters. `graph scan` refreshes host and
process inventories alongside adapter inventory. Inspect
stored records with `ctx graph vertices shell`, `ctx graph vertices filesystem`,
or `ctx graph vertices webview`.
Host resolution matches operation requirements and refreshes stale observations.
Shell launches and protected file exports use graph preparation to validate the
selected executable or destination before use. Explicit choices take precedence.
Webview preparation requires a compatibility validator from its embedding backend.
See [host discovery](docs/host-discovery.md) for platform coverage and the library API.

Third-party adapters use the same API. Installing one directly leaves it
untrusted until you review and trust it:

```sh
ctx adapter test ./my-adapter
ctx adapter install ./my-adapter
ctx adapter trust my-adapter
```

A bare ctx binary can install a prebuilt `.ctxadapter` archive or a
checksum-pinned HTTPS adapter index without Go. The system graph records
observations from installed, trusted adapters; it does not assume every tool
is present. See [adapter packaging](docs/adapter-api.md#binary-packages-and-go-builds)
and [the graph design](docs/adr-graph-runtime.md).

## Go libraries

CTX and its ecosystem share public Go libraries:

- `github.com/webong/ctx/graph` supplies generic graph storage and transactions.
- `github.com/webong/ctx/plugin` supplies the shared host/guest contract,
  validation, selection, admission, and session lifecycle. Its implementations
  include `plugin/inprocess`, `plugin/jsonline`, `plugin/hashicorp` (net/rpc and
  gRPC), `plugin/nativego`, `plugin/wasm` (WASI Preview 1), and `plugin/cshared`
  (versioned C ABI). All use the same typed authoring and host session APIs.
- `github.com/webong/ctx/supervisor` supplies local process supervision when
  the selected plugin backend does not already own its process lifecycle.

CTX adapters, Xallet, Cymonkey, and other applications can build on these
libraries. Domain behavior and authorization remain with each consumer.
See the [plugin contract](docs/adr-plugin-contract.md) and the
[HashiCorp backend guide](plugin/hashicorp/README.md). The
[runtime authoring guide](docs/plugin-runtimes.md) shows one implementation built
as a Go plugin, a WASI command, or a C shared library.

## Documentation

- [Browser cookies and automation input](docs/browser-cookies.md)
- [Browser profile and page management](docs/browser-management.md)
- [Native credentials](docs/credentials.md)
- [Extension distribution](docs/extension-distribution.md)
- [Build a CTX adapter](docs/adapter-authoring.md)
- [Adapter API and packaging](docs/adapter-api.md)
- [Cross-platform support](docs/cross-platform.md)
- [System graph design](docs/adr-graph-runtime.md)
- [Shared plugin contract and consumer integration](docs/adr-plugin-contract.md)

## License

[MIT](LICENSE)

### Plugin SDK authoring and tooling

The [plugin SDK guide](docs/plugin-sdk.md) covers typed Go methods, a TypeScript
host/guest SDK, instance leases, optional health/configuration/stream contracts,
package manifests, dependency graphs and conformance tests. Runtime backends
remain inside the reusable plugin library, including HashiCorp go-plugin.

```sh
go run ./examples/plugin-sdk --backend jsonline
go run ./examples/plugin-typescript
go run ./cmd/ctx-plugin inspect --root examples/plugin-package --entry main examples/plugin-package/plugin.json
```

Inspection validates metadata and content without starting plugins. CTX adapters
retain their native bindings. Xallet and Cymonkey adoption is separate work.
