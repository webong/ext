#!/usr/bin/env python3
"""Plans a CI run for one workflow using .github/ci/areas.json and .github/ci/releases.json.

Selection, by event:
  push to main, or the label ci:full
      every tool on every platform; on a push, an area with main_paths runs only if a changed
      file matches them
  pull_request
      tool:<name> labels choose jobs by tool. Without them, area paths, job paths and suite
      target paths select the tests. platform:<name> labels choose platforms. Linux is the
      default, and jobs can select a native runner through pr_platform_paths
  workflow_dispatch
      as a pull request, with --labels standing in for the PR's labels
  release:<package> label
      add that package's dry-run builds to its owning workflow

A job marked "on_request" (slow, or needing a real device session) runs only with ci:full; one
marked "push_only" only on a push to main. A job with its own "tools" runs, when tool: labels pick,
only if a picked tool is among them (so tool:swift runs the Swift cells, not the area's Go ones).

Writes "cells=<json list of cells>" and "run=true|false" to $GITHUB_OUTPUT or stdout, and a
summary table to $GITHUB_STEP_SUMMARY when it is set. Standard library only.

Usage: plan.py --event EVENT --workflow core|engine|runtime [--labels 'a,b'] [--changed FILE]
       plan.py --check   (scripts exist, groups have one workflow, cells are runnable)
"""
import argparse
import fnmatch
import json
import os
import shlex
import sys

import release_plan


def platform_of(job, cell):
    if job.get("platform"):
        return job["platform"]
    runner = str(cell.get("runner") or "")
    for prefix, name in (("ubuntu", "linux"), ("macos", "macos"), ("windows", "windows")):
        if runner.startswith(prefix):
            return name
    raise SystemExit(f"cannot tell the platform of runner {runner!r}")


def matches(paths, changed):
    if paths is None:
        return True
    for name in changed:
        for pattern in paths:
            # GitHub's ** matches across directories; fnmatch's * already does.
            if fnmatch.fnmatchcase(name, pattern.replace("**", "*")):
                return True
    return False


def scope_job(area, job, tools, changed, shared):
    targets = area.get("targets", {}) if job.get("targeted") else {}
    job_tools = set(job.get("tools", area["tools"]))
    if tools:
        wanted = tools & job_tools
        return bool(wanted), set() if wanted - targets.keys() else wanted
    if (shared or matches(job.get("pr_paths", area["pr_paths"]), changed)
            or matches(job.get("pr_platform_paths", []), changed)):
        return True, set()
    selected = {name for name, target in targets.items() if matches(target["paths"], changed)}
    return bool(selected), selected


def scope_cell(cell, targets, selected):
    if not selected:
        return cell
    selected_setup = {key for name in selected for key in targets[name].get("setup", [])}
    unused_setup = {key for target in targets.values() for key in target.get("setup", [])} - selected_setup
    setup = {key: value for key, value in cell["setup"].items() if key not in unused_setup}
    names = sorted(selected)
    job_name = cell["name"].partition(" (")[0]
    details = [cell["runner"], cell["vars"].get("TOOLCHAIN")]
    details.extend(cell["vars"].get(name.upper()) for name in names)
    label = ", ".join(filter(None, details))
    return dict(cell, setup=setup, vars=dict(cell["vars"], TARGETS=" ".join(names)),
                name=f"{job_name} [{', '.join(names)}] ({label})")


def plan(table, event, labels, changed, workflow):
    tools_all = set(table["tools"])
    platforms_all = set(table["platforms"])
    tools = {l.split(":", 1)[1] for l in labels if l.startswith("tool:")}
    platforms = {l.split(":", 1)[1] for l in labels if l.startswith("platform:")}
    unknown = (tools - tools_all) | (platforms - platforms_all)
    if unknown:
        raise SystemExit(f"unknown labels: {sorted(unknown)} (tools {sorted(tools_all)}, platforms {sorted(platforms_all)})")
    full = event == "push" or "ci:full" in labels
    on_request = "ci:full" in labels
    shared = matches(table["shared_paths"], changed)
    use_default_platform = not full and not platforms
    if full:
        tools, platforms = tools_all, platforms_all
    elif not platforms:
        platforms = {"linux"}
    owned = table["workflows"][workflow]
    groups = {g: [] for g in owned}
    areas = {}
    for name, area in table["areas"].items():
        if full and event == "push" and not shared and not matches(area["main_paths"], changed):
            areas[name] = 0
            continue
        count = 0
        for job in area["jobs"].values():
            if job["group"] not in groups or (job.get("on_request") and not on_request) or (
                job.get("push_only") and event != "push"
            ):
                continue
            wanted, selected = (True, set()) if full else scope_job(area, job, tools, changed, shared)
            if not wanted:
                continue
            cells = [cell for cell in job["cells"] if platform_of(job, cell) in platforms]
            if not cells and use_default_platform and not tools and matches(job.get("pr_platform_paths", []), changed):
                cells = job["cells"]
            for cell in cells:
                cell = scope_cell(cell, area.get("targets", {}), selected)
                # Export the matrix values without requiring jq or Python on the runner.
                exports = "; ".join(f"export MATRIX_{k}={shlex.quote(v)}" for k, v in sorted(cell["vars"].items()))
                groups[job["group"]].append(dict(cell, exports=exports))
                count += 1
        areas[name] = count
    return groups, areas, sorted(tools), sorted(platforms), full


def check(table):
    problems = []
    groups = set(table["groups"])
    owners = {}
    for workflow, owned in table["workflows"].items():
        if not os.path.isfile(f".github/workflows/{workflow}.yml"):
            problems.append(f"workflow {workflow}.yml does not exist")
        for g in owned:
            owners.setdefault(g, []).append(workflow)
    for g in groups:
        if len(owners.get(g, [])) != 1:
            problems.append(f"group {g} belongs to {owners.get(g, [])}, not exactly one workflow")
    names = set()
    for name, area in table["areas"].items():
        for target in area.get("targets", {}):
            if target not in area["tools"]:
                problems.append(f"{name}: {target} is not an area tool")
        for job_name, job in area["jobs"].items():
            if job.get("group") not in groups:
                problems.append(f"{name}/{job_name}: group {job.get('group')!r} is not one of {sorted(groups)}")
            if job.get("targeted") and not area.get("targets"):
                problems.append(f"{name}/{job_name}: targeted job has no targets")
            for cell in job["cells"]:
                platform_of(job, cell)
                if not os.path.isfile(cell["script"]):
                    problems.append(f"{cell['name']}: script {cell['script']} does not exist")
                if cell["name"] in names:
                    problems.append(f"{cell['name']}: two cells have this name")
                names.add(cell["name"])
    for problem in problems:
        print(problem, file=sys.stderr)
    return 1 if problems else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--event")
    parser.add_argument("--workflow", help="plan only the groups owned by core, engine, or runtime")
    parser.add_argument("--labels", default="")
    parser.add_argument("--changed", help="file listing changed paths, separated by NUL or newline")
    parser.add_argument("--areas", default=".github/ci/areas.json")
    parser.add_argument("--check", action="store_true", help="check areas.json against the scripts")
    args = parser.parse_args()
    with open(args.areas) as f:
        table = json.load(f)
    with open(".github/ci/releases.json") as f:
        releases = json.load(f)
    if args.check:
        return max(check(table), release_plan.check(releases))
    if not args.event or args.workflow not in table["workflows"]:
        parser.error(f"--event and --workflow (one of {sorted(table['workflows'])}) are required")
    labels = [l.strip() for l in args.labels.split(",") if l.strip()]
    changed = []
    if args.changed:
        with open(args.changed, "rb") as f:
            paths = f.read()
        entries = paths.split(b"\0") if b"\0" in paths else paths.splitlines()
        changed = [os.fsdecode(path) for path in entries if path]
    groups, areas, tools, platforms, full = plan(table, args.event, labels, changed, args.workflow)
    release_cells, packages = release_plan.dry_run_cells(releases, args.workflow, labels)
    cells = [cell for jobs in groups.values() for cell in jobs] + release_cells
    lines = [
        f"cells={json.dumps(cells, separators=(',', ':'))}",
        f"run={'true' if cells else 'false'}",
    ]
    out = os.environ.get("GITHUB_OUTPUT")
    if out:
        with open(out, "a") as f:
            f.write("\n".join(lines) + "\n")
    else:
        print("\n".join(lines))

    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a") as f:
            mode = "full run" if full else f"tools: {', '.join(tools) or 'from changed paths'}"
            f.write(f"### {args.workflow}.yml plan ({args.event}; {mode}; platforms: {', '.join(platforms)})\n\n")
            f.write("| Area | Cells |\n| --- | --- |\n")
            for name, count in areas.items():
                f.write(f"| {name} | {count} |\n")
            f.write(f"\nRelease dry runs: {', '.join(packages) or 'none'} ({len(release_cells)} cells).\n")
            f.write("\nUse `tool:<name>`, `platform:<name>`, `ci:full`, or `release:<package>` to change what runs.\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
