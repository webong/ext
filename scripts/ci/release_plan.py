#!/usr/bin/env python3
"""Routes a release.yml run to the packages of .github/ci/releases.json.

  push of a tag        the package whose tag prefix the tag starts with (the longest prefix wins);
                       an unknown tag fails, so a misspelt tag never passes silently
  workflow_dispatch    the package named by --package, as a dry run
  pull_request         the packages whose pr_paths match a changed file, as dry runs

Writes "<package>=true|false" lines and "version=<dry-run version>" to $GITHUB_OUTPUT (or stdout).
Publishing is never decided here: each package's workflow publishes only on a tag push of its own
prefix. Standard library only.

Usage: release_plan.py --event EVENT [--ref-name NAME] [--package NAME] [--changed FILE]
       release_plan.py --check   (every package's workflow is reusable and named in release.yml)
"""
import argparse
import fnmatch
import json
import os
import sys


def route(table, event, ref_name, package, changed):
    packages = table["packages"]
    chosen = set()
    if event == "push":
        best = None
        for name, p in packages.items():
            for prefix in p["tag_prefixes"]:
                if ref_name.startswith(prefix) and (best is None or len(prefix) > best[1]):
                    best = (name, len(prefix))
        if best is None:
            raise SystemExit(f"tag {ref_name!r} matches no package in releases.json")
        chosen.add(best[0])
    elif event == "workflow_dispatch":
        if package not in packages:
            raise SystemExit(f"unknown package {package!r}; one of {sorted(packages)}")
        if package == "binaries":
            raise SystemExit("the binaries release runs only for a pushed ctx-v, ctn-v or ext-v tag")
        chosen.add(package)
    elif event == "pull_request":
        for name, p in packages.items():
            paths = p["pr_paths"]
            if paths and any(fnmatch.fnmatchcase(c, pat.replace("**", "*")) for c in changed for pat in paths):
                chosen.add(name)
    else:
        raise SystemExit(f"unexpected event {event!r}")
    return {name: name in chosen for name in packages}


def check(table):
    with open(".github/workflows/release.yml") as f:
        router = f.read()
    problems = []
    for name, p in table["packages"].items():
        with open(p["workflow"]) as f:
            text = f.read()
        if "workflow_call:" not in text or "\n  push:" in text.split("\npermissions:", 1)[0]:
            problems.append(f"{p['workflow']}: must be called only through release.yml")
        if f"uses: ./{p['workflow']}" not in router:
            problems.append(f"release.yml: no job calls {p['workflow']}")
        for prefix in p["tag_prefixes"]:
            if f"'{prefix}*'" not in router:
                problems.append(f"release.yml: tag pattern {prefix}* is missing")
    for problem in problems:
        print(problem, file=sys.stderr)
    return 1 if problems else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--event")
    parser.add_argument("--ref-name", default="")
    parser.add_argument("--package", default="")
    parser.add_argument("--changed")
    parser.add_argument("--version", default="", help="dry-run version from a manual run")
    parser.add_argument("--table", default=".github/ci/releases.json")
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    with open(args.table) as f:
        table = json.load(f)
    if args.check:
        return check(table)
    if not args.event:
        parser.error("--event is required")
    changed = []
    if args.changed:
        with open(args.changed) as f:
            changed = [l.strip() for l in f if l.strip()]
    chosen = route(table, args.event, args.ref_name, args.package, changed)
    lines = [f"{name}={'true' if on else 'false'}" for name, on in chosen.items()]
    for name, on in chosen.items():
        default = table["packages"][name]["version_default"]
        if on and default is not None:
            lines.append(f"{name}_version={args.version or default}")
    out = os.environ.get("GITHUB_OUTPUT")
    if out:
        with open(out, "a") as f:
            f.write("\n".join(lines) + "\n")
    else:
        print("\n".join(lines))
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a") as f:
            picked = [n for n, on in chosen.items() if on] or ["none"]
            mode = "publishes if the tag's workflow accepts it" if args.event == "push" else "dry run"
            f.write(f"### Release ({args.event}; {mode})\n\nPackages: {', '.join(picked)}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
