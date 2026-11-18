#!/usr/bin/env python3
"""Plans a CI run: which areas of .github/ci/areas.json run, and which matrix cells of each job.

Selection, by event:
  push to main, schedule, or the label ci:full
      every tool on every platform; on a push, an area with main_paths runs only if a changed
      file matches them (as the per-area workflows did before ci.yml)
  pull_request
      tool:<name> labels choose the areas tagged with that tool; without one, the areas whose
      pr_paths match a changed file run. platform:<name> labels choose platforms; without one,
      only linux runs, so macOS, Windows, iOS and Android cost nothing unless a label asks
  workflow_dispatch
      as a pull request, with --labels standing in for the PR's labels

A job marked "nightly" in the table (slow, or needing a real device session) runs only on the
schedule or with ci:full, never on a push to main or an ordinary pull request.

Writes "<area>_run=true|false" and "<area>_cells=<json>" lines (job name -> list of cells) to
$GITHUB_OUTPUT, or to stdout, and a summary table to $GITHUB_STEP_SUMMARY when it is set.
Uses the standard library only.

Usage: plan.py --event EVENT [--labels 'a,b'] [--changed FILE] [--areas PATH]
       plan.py --check   (every workflow job is in areas.json and takes its cells from the plan)
"""
import argparse
import fnmatch
import json
import os
import re
import sys


def platform_of(job, cell):
    if job.get("platform"):
        return job["platform"]
    runner = str(cell.get("os") or job.get("runs-on") or "")
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
    full = event in ("push", "schedule") or "ci:full" in labels
    nightly = event == "schedule" or "ci:full" in labels
    if full:
        tools, platforms = tools_all, platforms_all
    elif not platforms:
        platforms = {"linux"}
    result = {}
    for name, area in table["areas"].items():
        if full:
            run = event != "push" or matches(area["main_paths"], changed)
        elif tools:
            run = bool(tools & set(area["tools"]))
        else:
            run = matches(area["pr_paths"], changed)
        cells = {}
        for job_name, job in area["jobs"].items():
            wanted = run and (nightly or not job.get("nightly"))
            kept = [c for c in job["cells"] if platform_of(job, c) in platforms] if wanted else []
            cells[job_name] = kept
        run = run and any(cells.values())
        result[name] = {"run": run, "cells": cells}
    return result, sorted(tools), sorted(platforms), full


def check(table):
    """Every job of an area's workflow is in the table, and reads its cells from the plan."""
    problems = []
    for name, area in table["areas"].items():
        with open(area["workflow"]) as f:
            text = f.read()
        if "workflow_call:" not in text:
            problems.append(f"{area['workflow']}: not a reusable workflow")
        jobs_text = text.split("\njobs:\n", 1)[1]
        jobs = re.findall(r"(?m)^  ([a-z0-9_-]+):\n", jobs_text)
        for job in jobs:
            if job not in area["jobs"]:
                problems.append(f"{area['workflow']}: job {job} is not in areas.json")
            elif f"fromJSON(inputs.cells)['{job}']" not in jobs_text:
                problems.append(f"{area['workflow']}: job {job} does not take its cells from the plan")
        for job in area["jobs"]:
            if job not in jobs:
                problems.append(f"areas.json: {name} lists job {job}, which its workflow lacks")
            for cell in area["jobs"][job]["cells"]:
                platform_of(area["jobs"][job], cell)
    for problem in problems:
        print(problem, file=sys.stderr)
    return 1 if problems else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--event")
    parser.add_argument("--check", action="store_true", help="check areas.json against the workflows")
    parser.add_argument("--labels", default="")
    parser.add_argument("--changed", help="file listing changed paths, one per line")
    parser.add_argument("--areas", default=".github/ci/areas.json")
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
    result, tools, platforms, full = plan(table, args.event, labels, changed)

    lines = []
    for name, r in result.items():
        lines.append(f"{name}_run={'true' if r['run'] else 'false'}")
        lines.append(f"{name}_cells={json.dumps(r['cells'], separators=(',', ':'))}")
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
            f.write("| Area | Runs | Cells |\n| --- | --- | --- |\n")
            for name, r in result.items():
                count = sum(len(c) for c in r["cells"].values())
                f.write(f"| {name} | {'yes' if r['run'] else 'no'} | {count} |\n")
            f.write("\nAdd `tool:<name>` or `platform:<name>` labels, or `ci:full`, to change what runs.\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
