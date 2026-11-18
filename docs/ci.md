# CI

Tests run from one workflow, `.github/workflows/ci.yml`. Releases run from their own workflows (see
[module versioning](module-versioning.md)).

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
| pull request labelled `ci:full` | all | all |
| manual run | as a pull request, from `-f labels=...` | as a pull request |

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
