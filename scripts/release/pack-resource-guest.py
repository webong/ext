#!/usr/bin/env python3
"""Seal one published single-file resource guest as an Ext package and zip."""

import hashlib
import json
from pathlib import Path
import shutil
import sys
import zipfile


def pack(template: Path, executable: Path, output: Path, target_os: str, arch: str) -> None:
    manifest = json.loads(template.read_text(encoding="utf-8"))
    artifacts = manifest["artifacts"]
    if len(artifacts) != 1 or artifacts[0]["path"] != executable.name:
        raise ValueError("template must name the published executable as its sole artifact")
    if not executable.is_file() or output.exists():
        raise ValueError("published executable is missing or output already exists")
    output.mkdir(parents=True)
    target = output / executable.name
    shutil.copy2(executable, target)
    digest = hashlib.sha256(target.read_bytes()).hexdigest()
    manifest["identity"]["revision"] = digest
    artifacts[0].update({"sha256": digest, "os": target_os, "arch": arch})
    (output / "ext.json").write_text(
        json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
    )
    with zipfile.ZipFile(str(output) + ".zip", "x", zipfile.ZIP_DEFLATED) as archive:
        for name in ("ext.json", executable.name):
            archive.write(output / name, name)


if __name__ == "__main__":
    if len(sys.argv) != 6:
        raise SystemExit(
            "usage: pack-resource-guest.py TEMPLATE PUBLISHED_EXE NEW_PACKAGE_DIR OS ARCH"
        )
    pack(Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]), sys.argv[4], sys.argv[5])
