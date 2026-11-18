#!/usr/bin/env python3
"""Plans a CI run: which cells of .github/ci/areas.json run, grouped by the ci.yml job that runs
them (go, engines, languages, conformance, platforms).

Selection, by event:
  push to main, or the label ci:full
      every tool on every platform; on a push, an area with main_paths runs only if a changed
      file matches them
  pull_request
      tool:<name> labels choose the areas tagged with that tool; without one, the areas whose
      pr_paths match a changed file run. platform:<name> labels choose platforms; without one,
      only linux runs, so macOS, Windows, iOS and Android cost nothing unless a label asks
  workflow_dispatch
      as a pull request, with --labels standing in for the PR's labels

A job marked "on_request" (slow, or needing a real device session) runs only with ci:full; one
marked "push_only" only on a push to main.

Writes "<group>=<json list of cells>" and "<group>_run=true|false" lines to $GITHUB_OUTPUT, or to
stdout, and a summary table to $GITHUB_STEP_SUMMARY when it is set. Standard library only.

Usage: plan.py --event EVENT [--labels 'a,b'] [--changed FILE] [--areas PATH]
       plan.py --check   (the table names existing scripts, known groups, and runnable cells)
"""
import argparse
import fnmatch
import json
import os
import shlex
import sys


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


def plan(table, event, labels, changed):
    tools_all = set(table["tools"])
    platforms_all = set(table["platforms"])
    tools = {l.split(":", 1)[1] for l in labels if l.startswith("tool:")}
    platforms = {l.split(":", 1)[1] for l in labels if l.startswith("platform:")}
    unknown = (tools - tools_all) | (platforms - platforms_all)
    if unknown:
        raise SystemExit(f"unknown labels: {sorted(unknown)} (tools {sorted(tools_all)}, platforms {sorted(platforms_all)})")
    full = event == "push" or "ci:full" in labels
    on_request = "ci:full" in labels
    if full:
        tools, platforms = tools_all, platforms_all
    elif not platforms:
        platforms = {"linux"}
    groups = {g: [] for g in table["groups"]}
    areas = {}
    for name, area in table["areas"].items():
        if full:
            run = event != "push" or matches(area["main_paths"], changed)
        elif tools:
            run = bool(tools & set(area["tools"]))
        else:
            run = matches(area["pr_paths"], changed)
        count = 0
        for job in area["jobs"].values():
            wanted = run and (on_request or not job.get("on_request")) and (event == "push" or not job.get("push_only"))
            if not wanted:
                continue
            for cell in job["cells"]:
                if platform_of(job, cell) in platforms:
                    # The job's shell reads the cell's values from one variable, so no runner needs
                    # jq or python to turn a JSON object into environment variables.
                    exports = "; ".join(f"export MATRIX_{k}={shlex.quote(v)}" for k, v in sorted(cell["vars"].items()))
                    groups[job["group"]].append(dict(cell, exports=exports))
                    count += 1
        areas[name] = count
    return groups, areas, sorted(tools), sorted(platforms), full


def check(table):
    problems = []
    groups = set(table["groups"])
    names = set()
    for name, area in table["areas"].items():
        for job_name, job in area["jobs"].items():
            if job.get("group") not in groups:
                problems.append(f"{name}/{job_name}: group {job.get('group')!r} is not one of {sorted(groups)}")
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
    parser.add_argument("--labels", default="")
    parser.add_argument("--changed", help="file listing changed paths, one per line")
    parser.add_argument("--areas", default=".github/ci/areas.json")
    parser.add_argument("--check", action="store_true", help="check areas.json against the scripts")
    args = parser.parse_args()
    with open(args.areas) as f:
        table = json.load(f)
    if args.check:
        return check(table)
    if not args.event:
        parser.error("--event is required")
    labels = [l.strip() for l in args.labels.split(",") if l.strip()]
    changed = []
    if args.changed:
        with open(args.changed) as f:
            changed = [l.strip() for l in f if l.strip()]
    groups, areas, tools, platforms, full = plan(table, args.event, labels, changed)

    lines = []
    for group, cells in groups.items():
        lines.append(f"{group}={json.dumps(cells, separators=(',', ':'))}")
        lines.append(f"{group}_run={'true' if cells else 'false'}")
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
            f.write(f"### CI plan ({args.event}; {mode}; platforms: {', '.join(platforms)})\n\n")
            f.write("| Area | Cells |\n| --- | --- |\n")
            for name, count in areas.items():
                f.write(f"| {name} | {count} |\n")
            f.write("\nAdd `tool:<name>` or `platform:<name>` labels, or `ci:full`, to change what runs.\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
