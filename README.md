# ext

Ext is a toolkit for building extensible software.

Use it to expose APIs, load plugins, and connect capabilities across implementations
and languages. Plugins can extend an application, use other plugins, and expose
extension points of their own.

Use the **CLI** to install and run plugins from your shell, or embed the
**libraries** in your own software.

## Use ext from the shell

`ext` is the public executable and a host for dynamically discovered command
plugins. Install packages, inspect what is available, then invoke the commands
they provide. Plugin-specific usage belongs in each plugin's documentation.

| Command | Purpose |
| --- | --- |
| `ext commands` | List available commands |
| `ext package` | Install, trust and manage plugin packages |
| `ext resource` | Discover providers, inspect resources and invoke explicitly declared operations |
| `ext source` | Manage cached package catalogs without granting execution trust |
| `ext bundle` | Plan and install pinned package groups; fetch native artifacts and generate bindings |
| `ext which <command>` | Show which implementation supplies a command |

### Install

On macOS or Linux:

```sh
curl -fsSL https://webong.dev/ext/install.sh | sh
```

On Windows, download and run [install.ps1](https://webong.dev/ext/install.ps1)
from PowerShell.

### Discover and invoke plugins

```sh
ext commands
ext which <command>
ext <command> [args]
```

`<command>` is a name supplied by an installed command plugin. The CLI resolves
and dispatches it through the plugin contract. Installing another compatible
command package extends the available commands without rebuilding the CLI.

All plugin packages share one default installed store. Existing installations
move to it with `ext package migrate`; old stores are retained as backups.
See [package management](docs/adr-cli-package-model.md) and
[command dispatch](docs/adr-ext-command.md) for migration, discovery and trust rules.

## A network of plugins

Plugins can be both providers and consumers. An adapter may depend on a
program's contracts; that program can use capabilities provided by other
adapters. One implementation can serve several consumers.

A **library dependency** is code imported when software is built. A **plugin
dependency** is a declared requirement fulfilled by another implementation at
runtime. Collaboration through public plugin contracts does not require
importing another program's private implementation.

Packages describe implementations and dependencies; hosts resolve compatible
implementations and apply trust and authorization before use. Installation or
contract compatibility alone does not grant permission. Hosts retain control
of selection, routing and lifecycle, including how missing dependencies and
failures are handled.

## Use ext in software

Embed the libraries you need; using them does not require running the CLI.
Your application defines its APIs, chooses implementations and owns policy.
Ext supplies reusable contracts, validation, package integrity, invocation and
lifecycle mechanisms.

| You want to… | Start with |
| --- | --- |
| Expose extension points or implement a plugin | [Plugin contract](docs/adr-plugin-contract.md), [runtime authoring](docs/plugin-runtimes.md) |
| Embed the native plugin engine | [`lib/plugin`](lib/plugin/README.md) |
| Use Go engines and runtime backends | [`pkg/ext-go`](pkg/ext-go), [engine selection and library loading](docs/adr-library-opener.md) |
| Use another language or a mobile platform | [SDKs and bindings](pkg/README.md#plugin-language-sdks-and-bindings) |
| Store a graph, record observations or supervise processes | [`lib/ledger`](lib/ledger/README.md) |
| Work with the local OS | [`res/system`](res/system/README.md) |
| Work with web content, WebViews or WebAssembly | [`res/web`](res/web/README.md) |
| Build code that runs inside a browser extension | [`pkg/ext-web`](pkg/ext-web/README.md) |
| Use intelligence contracts and mechanisms | [`res/int`](res/int/README.md) |

Plugin implementations can use supported process, in-process, WebAssembly,
HashiCorp or C ABI backends. Shared transport does not make unrelated APIs
compatible: extensions implement the contracts their host accepts.

Go libraries are independently versioned modules. Choose only the modules you
need, and use their published versions; the root `go.work` joins them for
repository development. Current source paths may precede publication. See
[module versioning](docs/module-versioning.md) for tags and distribution.

Programs under `src/` also export Go libraries. Their package guests expose
commands through the same plugin contract; the CLI does not import their
product modules.

The [extension-author DX ADR](docs/adr-extension-authoring-dx.md) describes the
next layer of tooling: discovery, scaffolding, application contract tests and
consistent diagnostics. It distinguishes that direction from implemented
engine and archive support.

## Repository map

| Directory | Responsibility | Main components |
| --- | --- | --- |
| [`lib/`](lib) | Native C libraries shared by language bindings | `plugin`: plugin engine; `ledger`: graph store, recorder and supervisor |
| [`pkg/`](pkg) | Public language SDKs, bindings and platform packages | Go, Rust, Zig, TypeScript, Python, Ruby, PHP, Lua, .NET; Web, Darwin and Android SDKs |
| [`res/`](res) | Reusable domain contracts and platform mechanisms | `system`: local OS; `web`: web content and runtimes; `int`: intelligence |
| [`src/`](src) | First-class plugins built on Ext, also available as Go libraries | `ctx`: contexts; `ctn`: content; `cto`: containment |
| [`cmd/ext/`](cmd/ext) | The public CLI | Package discovery, installation and command dispatch |
| [`adapters/`](adapters/README.md) | Integrations with specific products, providers, browsers and native tools | Native discovery, installation, activation and capability implementations |
| [`tests/`](tests) | Tests spanning libraries and languages | Cross-language conformance and integration checks |
| [`docs/`](docs) | Guides, contracts and architecture decisions | [Architecture](docs/adr-architecture.md), [CI](docs/ci.md), [adapter authoring](docs/adapter-authoring.md) |

`lib`, `pkg`, `res` and `src` are containers, not one shared package. Libraries
do not depend on product internals; products collaborate through plugin
contracts rather than importing one another's implementation.
Provider-specific behavior stays in adapters, while consumers choose policy.

## License

[MIT](LICENSE)
