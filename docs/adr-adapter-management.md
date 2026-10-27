# Adapter Management in the ext Composition Root

Status: accepted. This ADR documents the implemented design.

## Context

Adapters are external products, providers, browsers, or native services that `ext` subsystems (`ctx`, `ctn`, `ctr`, `cvt`, ...) discover, install, trust, and invoke. The adapter store (`pkg/ext-go/engine/host/adapter`) is a shared library with a single on-disk home (`~/.config/ext/adapters/`).

Historically, `ctx` owned the full adapter management CLI (`ctx adapter ls|available|add|install|trust|doctor|remove...`) because it was the first and primary subsystem. As `ext` evolved into a composition root hosting multiple subsystems, this created tension:

- `ctx` standalone binary includes full adapter management (correct for a standalone product).
- `ext` composes `ctx` and `ctn` as libraries, but `ext` has no `adapter` command — users must know to run `ctx adapter` even under `ext`.
- `ctn` exposes only `ctn adapter ls` (read-only).
- Future subsystems (`ctr`, `cvt`, ...) would each need adapter commands or delegate, leading to duplication.

An adapter is a **platform capability**, not a `ctx` capability. The subsystem that composes others (`ext`) should own adapter management.

## Decision

**Adapter management moves to `ext` as a builtin command.** The implementation is extracted to a shared internal package (`src/ctx/internal/adaptercmd`) so both standalone `ctx` and `ext` use the exact same code.

### Architecture

```
pkg/ext-go/engine/host/adapter/          ← shared store library (no CLI)
src/ctx/internal/adaptercmd/             ← NEW: shared CLI implementation
  ├── run.go                 ← entry point: Run(args, stdout, stderr) int
  ├── list.go
  ├── install.go
  ├── trust.go
  ├── doctor.go
  ├── remove.go
  └── ...

src/ctx/internal/app/app.go              ← registers "adapter" → adaptercmd.Run
cmd/ext/internal/adaptercmd/adapter.go   ← registers "ext adapter" → adaptercmd.Run
```

### Command Mapping

| User Invocation | Implementation |
|-----------------|----------------|
| `ctx adapter <sub> ...` | `adaptercmd.Run(sub, ...)` |
| `ext adapter <sub> ...` | `adaptercmd.Run(sub, ...)` |
| `ctn adapter ls` | unchanged (read-only, uses store directly) |

### Cross-Subsystem Visibility

`ext adapter ls` (and `ctx adapter ls`) shows which subsystems use each adapter:

```bash
$ ext adapter ls
NAME        RUNTIME     TRUSTED   USED BY
chromium    browser     yes       ctx, ctn
keychain    credential  yes       ctx
wasm        engine      yes       ctn
docker      container   no        ctr
```

The `USED BY` column is derived from each subsystem's declared adapter requirements (in its manifest or config).

### Subsystem Responsibilities

| Subsystem | Adapter Management | Adapter Usage |
|-----------|-------------------|---------------|
| `ctx` (standalone) | Via shared `adaptercmd` | Uses store library |
| `ext` | Via shared `adaptercmd` (builtin) | — |
| `ctn` | Read-only (`ctn adapter ls`) | Uses store library |
| `ctr`, `cvt`, ... (future) | None | Uses store library |

Only the **composition root** (`ext` or standalone `ctx`) exposes adapter management to users.

## Consequences

### Positive

- **Single source of truth** — adapters installed once, visible to all subsystems.
- **No duplication** — one CLI implementation shared by `ctx` and `ext`.
- **Unified CLI** — `ext adapter` works regardless of which subsystem you use.
- **Future-proof** — add `ctr`, `cvt` without adapter CLI duplication.
- **Cross-subsystem visibility** — see which adapters serve which subsystems.

### Negative

- `ctx` standalone binary grows slightly (adds `adaptercmd` package).
- `ext` gains a new builtin command (`adapter`).
- Migration required: move `ctx adapter` implementation to shared package.

### Migration

1. Create `src/ctx/internal/adaptercmd/` with all `ctx adapter *` logic extracted from `app.go` and `adapters.go`.
2. Update `src/ctx/internal/app/app.go` to call `adaptercmd.Run()`.
3. Add `cmd/ext/internal/adaptercmd/adapter.go` proxying to `adaptercmd.Run()`.
4. Register `adapter` builtin in `cmd/ext/main.go`.
5. `ctn adapter ls` remains unchanged (uses store directly).

## Related

- [ADR: One `ext` command, with ctx and ctn as libraries](adr-ext-command.md)
- [Adapter ownership](AGENTS.md#adapter-ownership) — "Anything specific to a product, provider, browser, or native tool belongs in its adapter package under `adapters/<name>/`"
- [Platform implementation rule](AGENTS.md#a-platform-implementation-of-a-portable-public-library-api-is-not-an-adapter-merely-because-it-calls-a-native-framework) — "An adapter represents a selectable external product... A platform implementation of a portable public-library API is not an adapter"