# ADR: Ext as the manager of the software on a machine, offered in layers

Status: living record, started 2026-11-26. This file mirrors the summary at the top of [issue #231](https://github.com/webong/ext/issues/231), which stays open to track the thinking on what ext is becoming. Updates land here as that thinking moves.

## Context

The README opens with "Ext is a toolkit for building extensible software." The code has outgrown that line:

- The C core now owns package execution graphs, the ledger (including machine process inventory and reconciliation), supervision and command scopes. Go is one host among equals, alongside Swift, Java, C#, Rust, Node and Zig.
- `ext` is becoming a dispatcher. Installing a command package extends the CLI without a rebuild.
- Packages are the unit of everything: pinned archives, sources kept separate from trust, a registry release path, capability-based install, sealed guests and package authoring.
- Around 60 adapters sit behind contracts. Many of them are package managers, toolchain managers or runtimes in their own right (`homebrew`, `nvm`, `node`, `python`, `jvm`, `php`, `xcode`, `docker`, `podman`, `kube`, `gcloud`, `aws`). The others are software that ext inventories and controls, such as browsers, keychains, `systemd` and display and audio stacks.

So the project needs a statement of what ext is, both to guide what gets polished first and to explain it to newcomers.

## Current position

**Ext is the manager of package managers, and of the software on a machine.** It gives one inventory, one trust model and one way to install, update and run software across every package manager and installer. It does not replace those managers.

Ext delivers this as a stack. Each layer is useful on its own, and each is the foundation for the layer above it.

| Layer | What ext offers | Who uses it directly | Where it lives |
|---|---|---|---|
| **5. Experience** | One CLI: `ext commands`, `ext which`, `ext <command>`. Later, agent and IDE integrations | End users, scripts, agents | `cmd/ext` |
| **4. Machine management** | Inventory, install, update and govern software across package managers, under one trust model | Developers, teams, CI | `ext package` / `source` / `bundle` / `resource`, plus manager adapters |
| **3. Capability catalog** | Adapters that put browsers, containers, AI providers, keychains, audio, display and SDKs behind contracts | Anyone who needs "a browser" or "a container runtime" without caring which one | [Adapter repositories](adapter-repositories.md): `xallet/tab`, `zip`, `vat`, `tip` |
| **2. Distribution** | Package format, pinned archives, sources, registry, sealed guests, authoring, provenance | Package authors, hosts | Package store, registry, authoring tooling |
| **1. Runtime** | Plugin contracts, invocation across languages, dependency resolution, trust and approval, supervision, ledger | App developers embedding ext | C core in `lib/` plus the language bindings |

Rules that hold at every layer:

- A layer depends only on the layers below it. The CLI is one host of the runtime, adapters are packages, and machine management is adapters plus the ledger.
- Each layer is a separate way in. An embedder can use layer 1 without the CLI. A shell user can stay at layer 5 without ever seeing the C core.
- Trust and the ledger run through every layer. Installing never grants permission to run, and the host keeps policy, selection and lifecycle.

## The four verbs

As the manager of managers, ext should offer the same verbs across every source:

1. **Inventory:** what is installed, from which manager, which version, and what owns it.
2. **Resolve and install:** "I need Node 22 and Postgres", with the provider chosen by host policy and by what is already present.
3. **Govern:** trust, pin, audit and approve across managers, with ledger reconciliation as drift detection.
4. **Act:** update, remove and run software consistently, without learning each tool's CLI.

## Positioning

| Tool | Overlap | Where ext differs |
|---|---|---|
| mise / asdf | Multi-runtime version management | Covers far more than runtimes, and adds trust and a ledger |
| topgrade | "Update everything" across managers | Has a model, inventory and policy, not just update commands |
| Nix / devbox | Reproducible environments | Wraps existing managers instead of replacing them |
| winget / Homebrew bundle | Declaring what to install | Works across managers and operating systems, and can be embedded |
| osquery | Machine inventory | Can act, not only observe |
| MCP | Capability exposure for agents | Adds packaging, trust, supervision and native execution |

## Consequences

- **README and docs** open with the manager-of-software framing. The plugin and runtime story explains how it works. Docs are organised by how people come in: "manage your machine" (layers 4–5), "write an adapter or package" (layers 2–3) and "embed ext" (layer 1).
- **Versioning splits by layer.** The layer-1 ABI carries the strongest stability promise. Layer-3 adapters version and release independently, as the standalone adapter moves already allow.
- **Adapter tiers.** A small set is kept excellent, with conformance tests against the real tools. The rest are marked community or experimental, which caps the maintenance burden.
- **The first visible feature is inventory across managers.** It is read-only, low risk, and immediately useful.

## Open questions

1. **Authoritative or observing?** Is ext the only path for changes, or does it watch and govern what other managers do? The proposal is to start observing: reconcile state from brew, pip, npm and the rest, and become authoritative for each capability only when hosts opt in.
2. **Capability identity across managers.** "Postgres from brew", "postgres in Docker" and "Postgres.app" have to resolve to the same capability with different providers. What is the identity model on top of capability-based install?
3. **The first managers to make excellent.** Proposed: Homebrew, npm/nvm, pip/python, Docker/Podman and one SDK (Xcode or Android).
4. **Privilege.** System managers (apt, systemd, OS installers) need root or admin. How does the trust model cover privilege escalation as well as permission to run?
5. **Lead story for adoption.** Is the flagship demo a shell user ("see everything on my machine") or an agent ("an agent installs a capability, the host approves it, the ledger records it")?
6. **Breadth vs depth.** How many bindings and adapters stay first-class while the core is still moving?

## Alternatives considered

- **Runtime only (embeddable plugin system).** This is accurate for layer 1 but undersells what users see, and it places ext in a crowded field (WASM component model, Extism, COM/XPCOM).
- **Catalog only (one CLI over machine tools).** This is useful, but without layers 1–2 it is a wrapper collection with an open-ended maintenance burden and no trust or distribution story.
- **Registry first.** A registry needs network effects that only the layers above it can bring.

Presenting these as layers of one system, rather than picking one, matches the architecture that already exists.
