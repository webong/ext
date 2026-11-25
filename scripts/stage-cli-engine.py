#!/usr/bin/env python3
"""Stage/verify the canonical C engine carried by an ext CLI target. Publishes nothing."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parent.parent
NAMES = {"darwin": "libext_plugin_host.dylib", "linux": "libext_plugin_host.so", "windows": "ext_plugin_host.dll"}
LEDGER_NAMES = {"darwin": "libext_ledger.dylib", "linux": "libext_ledger.so", "windows": "ext_ledger.dll"}
TARGETS = {f"{os}/{arch}" for os in NAMES for arch in ("amd64", "arm64")}


def digest(path):
    if not path.is_file() or path.stat().st_size > 256 * 1024 * 1024:
        raise ValueError(f"not a bounded regular file: {path}")
    return hashlib.sha256(path.read_bytes()).hexdigest()


def revision():
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("stage", "verify"))
    parser.add_argument("source", type=Path)
    parser.add_argument("target", choices=sorted(TARGETS))
    parser.add_argument("destination", type=Path)
    parser.add_argument("--ledger", type=Path, help="C ledger library (required when staging)")
    args = parser.parse_args()
    name = NAMES[args.target.split("/")[0]]
    ledger_name = LEDGER_NAMES[args.target.split("/")[0]]
    ledger_interface = ROOT / "lib/ledger/interface.json"
    expected_interface = ROOT / "lib/plugin/interface.json"
    if args.operation == "stage":
        if args.ledger is None:
            raise ValueError("staging requires --ledger from the same commit")
        args.destination.mkdir(parents=True, exist_ok=False)
        shutil.copyfile(args.source, args.destination / name)
        shutil.copyfile(expected_interface, args.destination / "interface.json")
        shutil.copyfile(args.ledger, args.destination / ledger_name)
        shutil.copyfile(ledger_interface, args.destination / "ledger-interface.json")
        record = {"version": 2, "target": args.target, "revision": revision(), "library": name,
                  "sha256": digest(args.destination / name), "interface_sha256": digest(expected_interface), "ledger": ledger_name,
                  "ledger_sha256": digest(args.destination / ledger_name), "ledger_interface_sha256": digest(ledger_interface)}
        (args.destination / "engine.json").write_text(json.dumps(record, indent=2) + "\n")
    else:
        source = args.source
        for file in ("engine.json", "interface.json", name, ledger_name, "ledger-interface.json"):
            if (source / file).is_symlink():
                raise ValueError(f"staged engine must not contain links: {source / file}")
        record = json.loads((source / "engine.json").read_text())
        if record != {"version": 2, "target": args.target, "revision": revision(), "library": name,
                      "sha256": digest(source / name), "interface_sha256": digest(expected_interface), "ledger": ledger_name,
                      "ledger_sha256": digest(source / ledger_name), "ledger_interface_sha256": digest(ledger_interface)}:
            raise ValueError(f"engine provenance, target, interface or digest mismatch for {args.target}")
        if (source / "interface.json").read_bytes() != expected_interface.read_bytes():
            raise ValueError(f"stale engine interface for {args.target}")
        if (source / "ledger-interface.json").read_bytes() != ledger_interface.read_bytes():
            raise ValueError("stale ledger interface")
        args.destination.mkdir(parents=True, exist_ok=False)
        for file in (name, "engine.json", "interface.json", ledger_name, "ledger-interface.json"):
            shutil.copyfile(source / file, args.destination / file)


if __name__ == "__main__":
    main()
