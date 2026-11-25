#!/usr/bin/env python3
"""Publish an adapter's prepared platform archives to a static Ext registry."""

import argparse
import json
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


def publish(ext, registry, version, archives):
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}", version, re.ASCII):
        raise ValueError("release version must be X.Y.Z")
    names = [archive.name for archive in archives]
    if any(not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]*\.(?:ctxadapter|zip|tar\.gz)", name) for name in names):
        raise ValueError("expected URL-safe .ctxadapter, .zip or .tar.gz archive names")
    if len(names) > 256 or len({name.lower() for name in names}) != len(names):
        raise ValueError("expected at most 256 archives with distinct names")

    registry.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".adapter-release-", dir=registry) as work:
        work = Path(work)
        release = work / "release"
        release.mkdir()
        for archive in archives:
            destination = release / archive.name
            shutil.copyfile(archive, destination)
            destination.chmod(0o644)

        index = release / "index.json"
        subprocess.run(
            [ext, "package", "index", str(index), *(str(release / name) for name in names)],
            check=True,
            stdout=subprocess.DEVNULL,
        )
        contents = index.read_bytes()
        source = json.loads(contents)
        adapter = registry / source["name"].replace("/", "~")
        relative = Path("releases") / version
        published = adapter / relative
        published.parent.mkdir(parents=True, exist_ok=True)
        # The immutable directory appears with all its archives and its index at once.
        try:
            release.rename(published)
        except OSError:
            existing = published / "index.json"
            if not existing.is_file():
                raise
            if existing.read_bytes() != contents:
                raise ValueError(f"release {published} already has different archives") from None

        # Cached source indexes can still refer to earlier releases after refresh.
        for package in source["packages"].values():
            package["url"] = (relative / package["url"]).as_posix()
        pending = work / "index.json"
        pending.write_text(json.dumps(source, indent=2) + "\n", encoding="utf-8")
        pending.chmod(0o644)
        current = adapter / "index.json"
        pending.replace(current)
        return current


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ext", default="ext", help="Ext CLI executable (default: ext on PATH)")
    parser.add_argument("registry", type=Path, help="local directory served by the registry")
    parser.add_argument("version", help="version to publish (X.Y.Z)")
    parser.add_argument("archives", type=Path, nargs="+")
    args = parser.parse_args()
    try:
        index = publish(args.ext, args.registry, args.version, args.archives)
    except (OSError, ValueError, subprocess.CalledProcessError) as err:
        print(f"adapter release: {err}", file=sys.stderr)
        return 1
    print(index)
    return 0


if __name__ == "__main__":
    sys.exit(main())
