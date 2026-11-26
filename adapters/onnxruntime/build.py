#!/usr/bin/env python3
"""Build the provider using a pinned published library, outside the workspace."""
import argparse
import hashlib
from pathlib import Path
import subprocess
import tempfile
import urllib.request

PACKAGE = "Ext.ML.Windows.0.1.0.nupkg"
URL = "https://github.com/webong/ext/releases/download/ext-windows-v0.1.0/" + PACKAGE
SHA256 = "f3887ef1f632a273a12745536a3e8f694a788730689ebe197c2890130c02d304"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("runtime", choices=["win-x64", "linux-x64"])
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent
    platform = "Windows" if args.runtime.startswith("win-") else "Linux"
    project = root / platform.lower() / f"Ext.ML.{platform}.Guest.csproj"
    with tempfile.TemporaryDirectory(prefix="ext-onnx-feed-") as temporary:
        feed = Path(temporary)
        data = urllib.request.urlopen(URL, timeout=120).read()
        if hashlib.sha256(data).hexdigest() != SHA256:
            raise SystemExit("published library checksum mismatch")
        (feed / PACKAGE).write_bytes(data)
        subprocess.run([
            "dotnet", "publish", str(project), "-c", "Release", "-r", args.runtime,
            "--self-contained", "true", "-p:PublishSingleFile=true",
            "-p:IncludeNativeLibrariesForSelfExtract=true",
            "--source", str(feed), "--source", "https://api.nuget.org/v3/index.json",
            "-o", str(args.output.resolve()),
        ], check=True)


if __name__ == "__main__":
    main()
