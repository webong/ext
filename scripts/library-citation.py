#!/usr/bin/env python3
"""Writes the citation of one release of a C library of lib/: the file ext lib fetch reads (the
format ext.library.citation/v1, cengine/library.Citation). It knows no library: a layout file names,
for each platform archive the release builds, the GOOS/GOARCH targets it serves and the files each
driver takes from it; this script adds the archive's URL, its SHA-256 (from checksums.txt) and its
root directory.

Usage: library-citation.py <layout.json> <version> <tag> <dist-dir> [base-url]
  layout.json  {"library": "ext_plugin", "archive": "ext-plugin-{version}-{platform}.tar.gz",
                "assets": [{"platform": "linux-x86_64", "targets": ["linux/amd64"], "drivers": {...}}]}
  base-url     where the release's files are published; default the repository's GitHub release
Writes <dist-dir>/<library>.citation.json.
"""
import json
import os
import re
import sys


def main():
    if len(sys.argv) not in (5, 6):
        sys.exit(__doc__)
    layout_file, version, tag, dist = sys.argv[1:5]
    repository = os.environ.get("GITHUB_REPOSITORY", "webong/ext")
    base = sys.argv[5] if len(sys.argv) == 6 else f"https://github.com/{repository}/releases/download/{tag}"
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        sys.exit(f"version {version!r} must look like 1.2.3")
    with open(layout_file) as f:
        layout = json.load(f)
    sums = {}
    with open(os.path.join(dist, "checksums.txt")) as f:
        for line in f:
            digest, name = line.split()
            sums[name.lstrip("*")] = digest
    assets = []
    for a in layout["assets"]:
        archive = layout["archive"].format(version=version, platform=a["platform"])
        if archive not in sums:
            sys.exit(f"{archive} is not in {dist}/checksums.txt")
        assets.append({
            "targets": a["targets"],
            "url": f"{base}/{archive}",
            "sha256": sums[archive],
            "root": archive[: -len(".tar.gz")],
            "drivers": a["drivers"],
        })
    citation = {"schema": "ext.library.citation/v1", "library": layout["library"], "version": version, "assets": assets}
    out = os.path.join(dist, layout["library"] + ".citation.json")
    with open(out, "w") as f:
        json.dump(citation, f, indent=2)
        f.write("\n")
    print("wrote", out)


if __name__ == "__main__":
    main()
