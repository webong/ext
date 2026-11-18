#!/usr/bin/env python3
"""Routes a release.yml run over the packages of .github/ci/releases.json.

  push of a tag        the package whose tag prefix the tag starts with (the longest prefix wins);
                       an unknown tag fails, so a misspelt tag never passes silently
  workflow_dispatch    the package named by --package, as a dry run
  pull_request         the packages whose pr_paths match a changed file, as dry runs

Writes to $GITHUB_OUTPUT (or stdout):
  build=<json list>   the build cells of the chosen packages, each with "exports" (its values as
                      shell export statements, MATRIX_<KEY> plus the dry-run version)
  build_run           true when there is a build cell
  package, prefix     the one package a tag push or manual run concerns (empty on pull requests)
  publish_setup       that package's toolchains for the publish job (JSON)
  registry            the registry job that publishes it next (crates, npm, pypi, nuget or empty)
  distribute          true when it has a dist/ branch to push
Publishing is never decided here: every publish job also requires a push of a tag with the
package's own prefix. Standard library only.

Usage: release_plan.py --event EVENT [--ref-name NAME] [--package NAME] [--version X.Y.Z] [--changed FILE]
       release_plan.py --check   (every script a package names exists)
"""
import argparse
import fnmatch
import json
import os
import shlex
import sys

REGISTRIES = {"crates", "npm", "pypi", "nuget"}


def choose(table, event, ref_name, package, changed):
    packages = table["packages"]
    if event == "push":
        best = None
        for name, p in packages.items():
            for prefix in p["tag_prefixes"]:
                if ref_name.startswith(prefix) and (best is None or len(prefix) > best[1]):
                    best = (name, len(prefix), prefix)
        if best is None:
            raise SystemExit(f"tag {ref_name!r} matches no package in releases.json")
        return [best[0]], best[0], best[2]
    if event == "workflow_dispatch":
        if package not in packages:
            raise SystemExit(f"unknown package {package!r}; one of {sorted(packages)}")
        if not packages[package]["build"]:
            raise SystemExit(f"{package} builds only for a pushed {packages[package]['tag_prefixes'][0]} tag; it has no dry run")
        return [package], package, packages[package]["tag_prefixes"][0]
    if event == "pull_request":
        chosen = [name for name, p in packages.items()
                  if p["pr_paths"] and any(fnmatch.fnmatchcase(c, pat.replace("**", "*")) for c in changed for pat in p["pr_paths"])]
        return chosen, "", ""
    raise SystemExit(f"unexpected event {event!r}")


def check(table):
    problems = []
    for name, p in table["packages"].items():
        d = f"scripts/release/{name}"
        for script in ("prepare.sh", "release.sh") + (("distribute.sh",) if p["distribute"] else ()):
            if not os.path.isfile(f"{d}/{script}"):
                problems.append(f"{name}: {d}/{script} does not exist")
        for cell in p["build"]:
            for key in ("script", "android_emulator"):
                if key in cell and not os.path.isfile(cell[key]):
                    problems.append(f"{cell['name']}: {cell[key]} does not exist")
        if p["registry"] not in REGISTRIES | {None}:
            problems.append(f"{name}: registry {p['registry']!r} has no job (one of {sorted(REGISTRIES)})")
    with open(".github/workflows/release.yml") as f:
        router = f.read()
    for name, p in table["packages"].items():
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
    parser.add_argument("--version", default="", help="dry-run version from a manual run")
    parser.add_argument("--changed")
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
    chosen, package, prefix = choose(table, args.event, args.ref_name, args.package, changed)
    cells = []
    for name in chosen:
        p = table["packages"][name]
        version = args.version or p["version_default"] or ""
        for cell in p["build"]:
            exports = [f"export MATRIX_{k}={shlex.quote(v)}" for k, v in sorted(cell["vars"].items())]
            exports.append(f"export INPUT_VERSION_DISPATCH={shlex.quote(version)}")
            cells.append(dict(cell, exports="; ".join(exports)))
    p = table["packages"].get(package) or {}
    out = {
        "build": json.dumps(cells, separators=(",", ":")),
        "build_run": "true" if cells else "false",
        "package": package,
        "prefix": prefix,
        "publish_setup": json.dumps(p.get("publish_setup") or {}, separators=(",", ":")),
        "registry": p.get("registry") or "",
        "distribute": "true" if p.get("distribute") else "false",
    }
    lines = [f"{k}={v}" for k, v in out.items()]
    target = os.environ.get("GITHUB_OUTPUT")
    if target:
        with open(target, "a") as f:
            f.write("\n".join(lines) + "\n")
    else:
        print("\n".join(lines))
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a") as f:
            mode = "publishes if the tag is the package's own" if args.event == "push" else "dry run"
            f.write(f"### Release ({args.event}; {mode})\n\nPackages: {', '.join(chosen) or 'none'}; build cells: {len(cells)}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
