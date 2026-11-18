# CI

Tests run from two workflows, and releases from one (tag and version rules:
[module versioning](module-versioning.md)):

| Workflow | Asks | Jobs |
| --- | --- | --- |
| `.github/workflows/core.yml` | Is ext correct, and do its languages agree? | `base`, `conformance` |
| `.github/workflows/targets.yml` | Does it build and run on every engine, runtime and platform? | `engines`, `runtimes`, `platforms` |
| `.github/workflows/release.yml` | Release a package | see [Releases](#releases) |

Both test workflows run on every push to `main` and every pull request, side by side.

## How a run is planned

Each test workflow starts with a `plan` job, which runs `scripts/ci/plan.py --workflow core` (or
`targets`) over `.github/ci/areas.json`. That table lists every **area** (`native`, `plugin-engine`,
`c2goengine`, `host-cases`, `plugin-sdk`, `plugin-wamr`, `web-js`, `ext-php`, `ext-ruby`, `ext-lua`,
`dotnet-windows`), the tools it tests, the paths that concern it, and its jobs, each with its
**cells**. A cell is one run: its name, its runner, the toolchains it needs (`setup`), its values
(`vars`) and its script under `scripts/ci/jobs/<area>/<job>.sh`. The plan picks the cells; its
summary on the run page says how many per area.

Each **group** is one job, which runs its group's picked cells as one matrix; the table's
`workflows` says which workflow owns which group:

| Workflow | Job | Runs |
| --- | --- | --- |
| core | `base` | the Go host: library modules, the native shell libraries on each OS, products, the standalone and arch checks, the Go SDK |
| core | `conformance` | the cross-language suites: host cases in every language, and the foreign SDKs through the C engine |
| targets | `engines` | the C plugin engine on every toolchain, and its c2goengine translation |
| targets | `runtimes` | the PHP, Ruby, Lua, .NET and web JS SDKs, and the Java, Swift and .NET libraries of `res/` |
| targets | `platforms` | Android and iOS packaging, the WAMR hosts, the real desktop session |

Every cell does the same thing: check out, run `.github/actions/setup` with its `setup` values (one
composite action that knows every toolchain), export its `vars` as `MATRIX_<KEY>`, and run its
script with bash. A script is the old job's steps in order; `scripts/ci/jobs/lib.sh` gives them the
runner's handling of `$GITHUB_ENV` and `$GITHUB_PATH` between steps and helpers for MSYS2 and
PowerShell steps. A cell may also name an artifact to keep, the Android emulator to run a script
in, or `autocrlf_off` (Git keeps Windows line endings off before checkout).

| Event | Areas | Platforms |
| --- | --- | --- |
| push to `main` | those whose `main_paths` match a changed file (all, when `main_paths` is null) | all |
| pull request with `tool:` labels | those tagged with a labelled tool | `platform:` labels, else linux |
| pull request without `tool:` labels | those whose `pr_paths` match a changed file | `platform:` labels, else linux |
| pull request labelled `ci:full` | all, with the on-request jobs | all |
| manual run | as a pull request, from `-f labels=...` | as a pull request |

There is no scheduled run, to keep CI cost down. A job marked `"on_request": true` in `areas.json`
is slow or needs a real device session: the C engine fuzzer, the real PipeWire and Wayland desktop
session, the Android emulator suite and the iOS Simulator suite. It runs only when a pull request or
a manual run carries `ci:full`, never on a push to `main`. A job marked `"push_only": true` (the
standalone-module check, which needs published sibling versions) runs only on a push to `main`.

Adding or removing a label re-plans a pull request's run, and a newer run of the same pull request or
branch cancels the older one. macOS, Windows, iOS and Android cost nothing on a pull request unless a
`platform:` label asks for them; `main` still runs every platform.

## Labels

- `platform:linux`, `platform:macos`, `platform:windows`, `platform:ios`, `platform:android`
- `tool:go`, `tool:c`, `tool:rust`, `tool:javascript`, `tool:zig`, `tool:python`, `tool:ruby`,
  `tool:php`, `tool:lua`, `tool:csharp`, `tool:swift`, `tool:java`
- `ci:full`

A label that is not in `areas.json` fails the plan, so a typo is caught.

A manual run on a branch: `gh workflow run core.yml --ref <branch> -f labels=tool:php,platform:macos`
(or `targets.yml`).

A job may list its own `tools`, narrower than its area's: `tool:swift` then runs the Swift cells of
the `native` area, not its Go ones.

## Languages

Each language's own tests live in `scripts/lang/<language>.sh` (`test`, and commands such as `pack`
or `typecheck`), which the job scripts call. The cross-runtime suites
(`scripts/plugin-crosslang.sh`, `plugin-cengine.sh`, `plugin-interop.sh`, `plugin-wasi-core.sh`,
`host-cases.sh`) test the languages against each other and run in the `conformance` group.

## Adding to CI

No change here adds a workflow file.

- **A cell** (a version, an OS): add it to its job's `cells` in `areas.json`.
- **A job:** its script under `scripts/ci/jobs/<area>/`, and its entry (with `group` and `cells`)
  in `areas.json`.
- **A language:** a `scripts/lang/<language>.sh`, its toolchain's inputs and step in
  `.github/actions/setup/action.yml` if it needs a new one, its cells, a tool name in `areas.json`,
  and the `tool:` label.
- **A group:** its entry in `groups` and in one workflow's list in `workflows`, and a job in that
  workflow, copied from the others.

`plan.py --check`, which the plan job runs, fails when a cell names a script that does not exist, a
group that has no job, or a runner whose platform it cannot tell.

## Releases

`release.yml` routes every run through `scripts/ci/release_plan.py` and `.github/ci/releases.json`,
which lists each package's tag prefixes, its workflow, the paths whose change makes a pull request
dry-run it, and its default dry-run version.

| Run | Packages | Publishes |
| --- | --- | --- |
| tag push | the package whose prefix the tag starts with (the longest prefix wins) | yes, if the package's workflow accepts the tag |
| pull request | the packages whose `pr_paths` match a changed file | no |
| `gh workflow run release.yml -f package=<name> [-f version=X.Y.Z]` | that package | no |

A tag that matches no package fails the route. Publishing is still decided inside each package's
workflow: its publish jobs run only for a tag push of its own prefix, in their own environments,
exactly as before they were routed. The `ctx-v`, `ctn-v` and `ext-v` binaries
(`release-binaries.yml`) release only from a tag.

Adding a package: its reusable workflow (`on: workflow_call`, taking `version` if it builds a
version by hand), its entry in `releases.json`, and its tag pattern and job in `release.yml`.
`release_plan.py --check`, which the route job runs, fails when they drift apart.
