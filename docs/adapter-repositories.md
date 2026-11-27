# Adapter repositories

Adapters are not part of ext. Each one integrates a specific product, provider,
browser or native tool, and ships as a sealed Ext package that a host installs,
trusts and invokes through its contract. They live in their own repositories:

| Repository | Adapters |
| --- | --- |
| [xallet/tab](https://github.com/xallet/tab) | Browsers (the `chromium` and `firefox` engines, Chrome, Edge, Brave, Safari, Zen and others), AI providers (`anthropic`, `openai`, `gemini`, `ollama`), coding agents (`claude_code`, `codex`) and model runtimes (`coreml`, `onnxruntime`) |
| [xallet/zip](https://github.com/xallet/zip) | Package and version managers (`homebrew`, `nvm`), language runtimes (`node`, `python`, `jvm`, `php`), toolchains (`git`, `xcode`) and evaluators (`wasm`, `evm`) |
| [xallet/vat](https://github.com/xallet/vat) | Containers and virtual machines (`docker`, `podman`, `nerdctl`, `apple`, `orbstack`, `rancher_desktop`, `docker_desktop`), clusters and cloud (`kube`, `aws`, `gcloud`) and databases (`mysql`, `postgres`) |
| [xallet/tip](https://github.com/xallet/tip) | Audio (`alsa`, `pipewire`, `pulseaudio`), display (`wayland`, `x11`), secrets (`keychain`, `secret_service`, `credman`), `systemd` and `ffmpeg` |

The adapters moved out of ext's `adapters/` directory with their history. ext
keeps the libraries, contracts, package trust and invocation they build on; see
[adapter ownership](../AGENTS.md#adapter-ownership) for where the boundary lies.

## Names

An adapter's Go module is named `xallet.app/<repository>/<adapter>`, for example
`xallet.app/tab/chromium`. Adapters ship through the Ext package registry, not
`go get`, so these names are identifiers rather than fetchable module paths, and
the repositories carry no Go release tags. Each repository's `go.work` ties its
modules together and pins the ext modules they use.

## Testing ext against real adapters

Each adapter repository owns its tests, including the checks that run ext against
its adapters; ext's own CI never fetches them. Those checks read a directory of
adapters from `EXT_ADAPTERS`, and skip when it is unset. `scripts/adapters.sh`
clones the four repositories (with your credentials) and copies their adapters
into one directory:

```sh
export EXT_ADAPTERS=$(scripts/adapters.sh)
go test ./src/ctx/internal/arch/
tests/native_test.sh
```

Tests that need adapters skip when `EXT_ADAPTERS` is unset. To work on ext and an
adapter together, point the script at local checkouts named `tab`, `zip`, `vat`
and `tip`:

```sh
export EXT_ADAPTERS=$(EXT_ADAPTERS_LOCAL=~/src/xallet scripts/adapters.sh)
```

Go adapters build from their repository checkout, where its `go.work` applies.
The directory's `.repos` file maps each adapter to that checkout.
