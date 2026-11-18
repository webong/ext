# CI

Tests run from one workflow, `.github/workflows/ci.yml`, and releases from one,
`.github/workflows/release.yml` (tag and version rules: [module versioning](module-versioning.md)).

## How a run is planned

`ci.yml` starts with a `plan` job. It runs `scripts/ci/plan.py` over `.github/ci/areas.json`, which
lists every **area** (a reusable workflow), the tools it tests, the paths that concern it, and each of
its jobs with the matrix cells it can run. The plan picks the areas and the cells, and each area's
workflow runs only those cells. Its summary on the run page says what was picked.

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
a manual run carries `ci:full`, never on a push to `main`.

Adding or removing a label re-plans a pull request's run, and a newer run of the same pull request or
branch cancels the older one. macOS, Windows, iOS and Android cost nothing on a pull request unless a
`platform:` label asks for them; `main` still runs every platform.

## Labels

- `platform:linux`, `platform:macos`, `platform:windows`, `platform:ios`, `platform:android`
- `tool:go`, `tool:c`, `tool:rust`, `tool:javascript`, `tool:zig`, `tool:python`, `tool:ruby`,
  `tool:php`, `tool:lua`, `tool:csharp`, `tool:swift`, `tool:java`
- `ci:full`

A label that is not in `areas.json` fails the plan, so a typo is caught.

A manual run on a branch: `gh workflow run ci.yml --ref <branch> -f labels=tool:php,platform:macos`.

## Languages

Each language's own tests live in `scripts/lang/<language>.sh` (`test`, and commands such as `pack`
or `typecheck`); workflows install the toolchain and call the script. The cross-runtime suites
(`scripts/plugin-crosslang.sh`, `plugin-cengine.sh`, `plugin-interop.sh`, `plugin-wasi-core.sh`,
`host-cases.sh`) test the languages against each other and stay areas of their own.

## Adding to CI

- **A job or a cell:** add it to its workflow and to `areas.json`. A job's matrix is
  `include: ${{ fromJSON(inputs.cells)['<job>'] }}`, so a cell missing from `areas.json` never runs.
- **A language:** a `scripts/lang/<language>.sh`, a tool name in `areas.json`, and the label.
- **An area:** a reusable workflow (`on: workflow_call` with the `cells` input), its entry in
  `areas.json`, and a job plus two plan outputs in `ci.yml`.

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
