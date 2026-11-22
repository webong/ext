# CI

Core, Engine, and Runtime run on every push to `main` and every pull request. Each runs its selected cells in parallel and reports one stable result check for the workflow.

| Workflow | Asks | Jobs |
| --- | --- | --- |
| Core (`core.yml`) | Do the Go libraries, products, and cross-language suites pass? | `base`, `conformance` |
| Engine (`engine.yml`) | Does the engine work across toolchains and architectures? | `engines` |
| Runtime (`runtime.yml`) | Do the SDKs and native platform integrations work? | `runtimes`, `platforms` |

GitHub shows each matrix cell as a check with its own runner and logs. Branch rules can require the
three stable result checks and use them across changes to the matrix.

## How a run is planned

The three workflows call `.github/workflows/ci.yml`, which runs `scripts/ci/plan.py` for the caller
using `.github/ci/areas.json`. That table lists every **area** (`native`, `plugin-engine`,
`c2goengine`, `host-cases`, `ledger`, `plugin-sdk`, `plugin-wamr`, `web-js`, `ext-php`, `ext-ruby`, `ext-lua`,
`dotnet-windows`), the tools it tests, the paths that concern it, and its jobs, each with its
**cells**. A cell is one run: its name, its runner, the toolchains it needs (`setup`), its values
(`vars`) and its script under `scripts/ci/jobs/<area>/<job>.sh`. The plan picks the cells; its
summary on the run page says how many per area.

The table's `workflows` map assigns each group to a workflow. One matrix per workflow runs the
selected cells from all its groups and any requested release dry runs.

| Workflow | Group | Runs |
| --- | --- | --- |
| core | `base` | the Go host: library modules, the native shell libraries on each OS, products, the standalone and arch checks, the Go SDK |
| core | `conformance` | the cross-language suites: host cases in every language, and the foreign SDKs through the C engine |
| engine | `engines` | the C plugin engine on every toolchain, and its c2goengine translation |
| runtime | `runtimes` | the PHP, Ruby, Lua, .NET and web JS SDKs, and the Java, Swift and .NET libraries of `res/` |
| runtime | `platforms` | Android and iOS packaging, the WAMR hosts, the real desktop session |

Every cell does the same thing: check out, run `.github/actions/setup` with its `setup` values (one
composite action that knows every toolchain), export its `vars` as `MATRIX_<KEY>`, and run its
script with bash. A script is the old job's steps in order; `scripts/ci/jobs/lib.sh` gives them the
runner's handling of `$GITHUB_ENV` and `$GITHUB_PATH` between steps and helpers for MSYS2 and
PowerShell steps. A cell may also name an artifact to keep, the Android emulator to run a script
in, or `autocrlf_off` (Git keeps Windows line endings off before checkout).

On a Ruby-only PR, the planner runs the Ruby SDK matrix and the Ruby portion of the host cases.
The PHP, Lua, Python, .NET, and TypeScript SDKs have their own path rules. Shared engine changes
and conformance fixtures can select several suites. Jobs with `pr_platform_paths` can select their
native runners for those paths when a PR has no explicit platform label.

| Event | Areas | Platforms |
| --- | --- | --- |
| push to `main` | those whose `main_paths` match a changed file (all, when `main_paths` is null) | all |
| pull request with `tool:` labels | those tagged with a labelled tool | `platform:` labels, else linux |
| pull request without `tool:` labels | jobs whose area paths, job paths, or suite target paths match | `platform:` labels, else Linux plus applicable native runners |
| pull request labelled `ci:full` | all, with the on-request jobs | all |
| manual run | as a pull request, from `-f labels=...` | as a pull request |

There is no scheduled run, to keep CI cost down. A job marked `"on_request": true` in `areas.json`
is slow or needs a real device session: the C engine fuzzer, the real PipeWire and Wayland desktop
session, the Android emulator suite and the iOS Simulator suite. It runs only when a pull request or
a manual run carries `ci:full`, never on a push to `main`. A job marked `"push_only": true` (the
standalone-module check, which needs published sibling versions) runs only on a push to `main`.

Adding or removing a label re-plans a pull request's run, and a newer run of the same pull request or
branch cancels the older one. Pull request tests use Linux by default. Platform labels, `ci:full`,
and release dry runs can schedule other runners. Main tests cover every platform.

## Labels

- `platform:linux`, `platform:macos`, `platform:windows`, `platform:ios`, `platform:android`
- `tool:go`, `tool:c`, `tool:rust`, `tool:javascript`, `tool:zig`, `tool:python`, `tool:ruby`,
  `tool:php`, `tool:lua`, `tool:csharp`, `tool:swift`, `tool:java`
- `ci:full`
- `release:<package>` adds that package's dry-run build cells to its owning workflow.

An unknown `tool:`, `platform:`, or `release:` label fails planning.

For a manual PHP run on macOS, use
`gh workflow run runtime.yml --ref <branch> -f labels=tool:php,platform:macos`.

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
  in `areas.json`. Use job-level `pr_paths` when it has narrower dependencies than the area.
  For a shared suite with independent test sections, define the area's `targets` and mark the job
  `targeted`. The script reads `MATRIX_TARGETS` for focused PR runs and runs all sections by default.
- **A language:** a `scripts/lang/<language>.sh`, its toolchain's inputs and step in
  `.github/actions/setup/action.yml` if it needs a new one, its cells, a tool name in `areas.json`,
  and the `tool:` label.
- **A group:** add its entry to `groups` and to its owner's list in `workflows`.

The plan job validates both tables and checks for missing scripts, invalid group ownership, and
unknown runner platforms.

## Releases

Add `release:<package>` to a pull request to rehearse packaging in the package's owning workflow.
For example, `release:ext` runs in Core, `release:ext-plugin` runs in Engine, and
`release:ext-zig` and `release:ext-ios` run in Runtime. Multiple labels can request multiple
packages. Release cells run on their configured runners, including when a PR has `tool:` or
`platform:` labels.

The package table in `.github/ci/releases.json` stores each tag prefix, build matrix, and publish
settings. Core owns packages without an explicit workflow. For packages that build in
`prepare.sh`, the rehearsal runs that script and uploads
its output with a dry-run version. For a manual Zig rehearsal, use
`gh workflow run runtime.yml --ref <branch> -f labels=release:ext-zig`.

Actual publishing runs through `.github/workflows/release.yml` on version tags or manual tag
recovery. Keeping the workflow at that path preserves the registry trusted-publisher identity.
See [module versioning](module-versioning.md) for tag and version rules.

| Run | Packages | Publishes |
| --- | --- | --- |
| tag push | the package whose prefix the tag starts with (the longest prefix wins) | yes |
| pull request with `release:<package>` | the labelled packages in Core, Engine, or Runtime | no |
| `gh workflow run release.yml -f package=<name> [-f version=X.Y.Z]` | that package | no |
| `gh workflow run release.yml -f package=<name> -f tag=<tag>` | that package, which must own the tag | yes, as the tag's push would |

The jobs, the same for every package:

| Job | Does |
| --- | --- |
| `build` | runs the package's build cells (`scripts/release/<package>/build.sh`, one cell per platform where it has several; the Android emulator where the cell names it) and uploads `release-out/` |
| `publish` | downloads the build into `dist/`, runs `prepare.sh` (verify the checksums, or for `ctx`, `ctn` and `ext` build and test), attests `dist/*`, and runs `release.sh` (the GitHub release) |
| `crates`, `npm`, `pypi`, `nuget` | the registry a package names, in that registry's environment |
| `distribute` | `distribute.sh`, which pushes the package's `dist/` branch (`ext-ios`, `ext-macos`, `ext-zig`) |

`publish` and `distribute` require a tag with the package's own prefix, pushed or named by a
manual publish. Each registry job also checks its package's literal tag prefix, and `nuget` needs
`EXT_NUGET_SOURCE`. A tag that matches no package fails routing.

A manual publish (`-f tag=<tag>`, with `version` empty) is for a tag whose push started no run.
GitHub sends no push event when more than three tags arrive in one push, and the ruleset forbids
deleting a tag to push it again, so push release tags one per `git push`. The run uses this
workflow from the branch it is started on, checks out the tagged commit in every job, routes the tag
as its push would, and gives the scripts the tag as `GITHUB_REF_NAME`.

To add a package, create its scripts under `scripts/release/<package>/`, assign it an owner in
`releases.json`, add its `release:<package>` label, and add its tag pattern to `release.yml`.
The publisher's route job validates the scripts and tag patterns.
