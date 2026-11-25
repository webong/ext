#!/usr/bin/env python3
"""Build and install this host's CLI archive in scratch space; publish nothing."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def run(*args, cwd=ROOT, env=None, success=True):
    result = subprocess.run(list(map(str, args)), cwd=cwd, env=env, text=True, capture_output=True)
    if (result.returncode == 0) != success:
        raise RuntimeError(f"unexpected exit {result.returncode}: {args}\n{result.stdout}\n{result.stderr}")
    return result


def snapshot(*roots):
    result = {}
    for root in roots:
        if not root.exists():
            continue
        for path in sorted(root.rglob("*")):
            info = path.lstat()
            if path.is_symlink():
                content = ("link", os.readlink(path))
            elif path.is_file():
                content = ("file", hashlib.sha256(path.read_bytes()).hexdigest())
            else:
                content = ("directory", "")
            result[str(path)] = (info.st_mode, content)
    return result


def main():
    build_env = {key: value for key, value in os.environ.items() if not key.startswith("EXT_")}
    for key in ("GOOS", "GOARCH", "GOWORK", "EXT_PLUGIN_LIBRARY", "EXT_LEDGER_LIBRARY"):
        build_env.pop(key, None)
    target = json.loads(run("go", "env", "-json", "GOHOSTOS", "GOHOSTARCH", env=build_env).stdout)
    system, arch = target["GOHOSTOS"], target["GOHOSTARCH"]
    if system not in ("darwin", "linux"):
        raise RuntimeError("this rehearsal covers the POSIX installer; Windows needs install.ps1")
    library = "libext_plugin_host.dylib" if system == "darwin" else "libext_plugin_host.so"
    with tempfile.TemporaryDirectory(prefix="ext-install-rehearsal-") as temporary:
        work = Path(temporary)
        engine, native, archives = work / "engine", work / "native", work / "archives"
        print(f"Building and rehearsing {system}/{arch}", flush=True)
        run("cmake", "-S", ROOT / "lib/plugin", "-B", engine, "-DCMAKE_BUILD_TYPE=Release", "-DEXT_BUILD_SHARED=ON", env=build_env)
        run("cmake", "--build", engine, "--target", "ext_plugin_host_shared", "-j", "4", env=build_env)
        ledger_build = work / "ledger"
        ledger_library = "libext_ledger.dylib" if system == "darwin" else "libext_ledger.so"
        run("cmake", "-S", ROOT / "lib/ledger", "-B", ledger_build, "-DEXT_LEDGER_SHARED=ON", "-DEXT_LEDGER_TESTS=OFF", env=build_env)
        run("cmake", "--build", ledger_build, "--target", "ext_ledger_shared", "-j", "4", env=build_env)
        run(sys.executable, ROOT / "scripts/stage-cli-engine.py", "stage", engine / library,
            f"{system}/{arch}", native / system / arch, "--ledger", ledger_build / ledger_library, env=build_env)
        release_env = dict(build_env, EXT_RELEASE_NATIVE_DIR=str(native), EXT_RELEASE_TARGETS=f"{system}/{arch}")
        run("sh", ROOT / "scripts/build-release-ext.sh", "0.0.0-acceptance", archives, env=release_env)
        archive = archives / f"ext-{system}-{arch}.tar.gz"
        checksums = archives / "checksums.txt"
        def checksum():
            checksums.write_text(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
        checksum()
        outside = work / "outside"
        outside.mkdir()
        # Copy only the installer: source installation cannot find the checkout.
        installer = outside / "install.sh"
        shutil.copyfile(ROOT / "install.sh", installer)
        home, binaries = work / "home", work / "bin"
        runtime = dict(build_env, GOWORK="off", EXT_INSTALL_SOURCE="0", EXT_VERSION="0.0.0-acceptance",
                       EXT_RELEASE_BASE=archives.as_uri(), EXT_BIN_DIR=str(binaries), EXT_HOME=str(home))
        run("sh", installer, cwd=outside, env=runtime)
        executable = binaries / "ext"
        run(executable, "package", "plan", cwd=outside, env=runtime)
        commands = run(executable, "commands", cwd=outside, env=runtime).stdout
        for product in ("ctx", "ctn", "cto"):
            if product not in commands:
                raise RuntimeError(f"installed product command missing: {product}")
            run(executable, product, "help", cwd=outside, env=runtime)
        run(executable, "ctx", "graph", "status", cwd=outside, env=runtime)
        run(executable, "ctx", "graph", "vertices", "shell-session", cwd=outside, env=runtime)
        before = snapshot(binaries, home)
        checksums.write_text(f"{'0' * 64}  {archive.name}\n")
        failed = run("sh", installer, cwd=outside, env=runtime, success=False)
        if "checksum mismatch" not in failed.stderr:
            raise RuntimeError("corrupt checksum did not fail at verification")
        if snapshot(binaries, home) != before:
            raise RuntimeError("failed verification changed installed files or approvals")
        # Even a checksum-valid archive must contain its canonical engine.
        unpack = work / "unpack"
        unpack.mkdir()
        # This is the archive just built above, not untrusted downloaded input.
        # Use the installer's tar tool so Python 3.11 is sufficient on Linux.
        run("tar", "-xzf", archive, "-C", unpack, env=build_env)
        ledger_file = unpack / "ext/bin/ext-lib" / ledger_library
        ledger_bytes = ledger_file.read_bytes()
        for contents in (b"not a native library", None):
            if contents is None:
                ledger_file.unlink()
            else:
                ledger_file.write_bytes(contents)
            with tarfile.open(archive, "w:gz") as package:
                package.add(unpack / "ext", arcname="ext")
            checksum()
            failed = run("sh", installer, cwd=outside, env=runtime, success=False)
            expected = "missing the C ledger" if contents is None else "C ledger unavailable"
            if expected not in failed.stderr:
                raise RuntimeError("broken or absent ledger was not refused before installation")
            if snapshot(binaries, home) != before:
                raise RuntimeError("invalid ledger changed installed files or approvals")
        ledger_file.write_bytes(ledger_bytes)
        (unpack / "ext/bin/ext-lib" / library).write_bytes(b"not a native library")
        with tarfile.open(archive, "w:gz") as package:
            package.add(unpack / "ext", arcname="ext")
        checksum()
        failed = run("sh", installer, cwd=outside, env=runtime, success=False)
        if "canonical plugin engine unavailable" not in failed.stderr:
            raise RuntimeError("broken engine was not refused before installation")
        if snapshot(binaries, home) != before:
            raise RuntimeError("broken engine changed installed files or approvals")
        shutil.rmtree(unpack / "ext/bin/ext-lib")
        with tarfile.open(archive, "w:gz") as package:
            package.add(unpack / "ext", arcname="ext")
        checksum()
        failed = run("sh", installer, cwd=outside, env=runtime, success=False)
        if "missing the canonical engine" not in failed.stderr:
            raise RuntimeError("engine-free archive was not refused")
        if snapshot(binaries, home) != before:
            raise RuntimeError("incomplete archive changed installed files or approvals")
        run(executable, "package", "plan", cwd=outside, env=runtime)
        print(f"PASS {system}/{arch}: archive install, bundled engine, product execution, checksum, broken/missing ledger and engine refusal", flush=True)


if __name__ == "__main__":
    main()
