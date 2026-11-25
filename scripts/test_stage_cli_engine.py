import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts/stage-cli-engine.py"


class CLIEngineStageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.library = self.root / "input.dylib"
        self.library.write_bytes(b"test engine bytes")
        self.staged = self.root / "native/darwin/arm64"
        self.call("stage", self.library, "darwin/arm64", self.staged, "--ledger", self.library)

    def call(self, *args, ok=True):
        result = subprocess.run([sys.executable, str(SCRIPT), *map(str, args)], capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, ok, result.stderr)
        return result

    def test_roundtrip_and_changed_library(self):
        destination = self.root / "bundle/ext-lib"
        self.call("verify", self.staged, "darwin/arm64", destination)
        self.assertEqual((destination / "libext_plugin_host.dylib").read_bytes(), self.library.read_bytes())
        (self.staged / "libext_plugin_host.dylib").write_bytes(b"changed")
        failed = self.root / "failed"
        self.call("verify", self.staged, "darwin/arm64", failed, ok=False)
        self.assertFalse(failed.exists())

    def test_wrong_target_and_stale_description(self):
        self.call("verify", self.staged, "darwin/amd64", self.root / "wrong", ok=False)
        (self.staged / "interface.json").write_text("{}")
        self.call("verify", self.staged, "darwin/arm64", self.root / "stale", ok=False)

    def test_missing_or_changed_ledger(self):
        ledger = self.staged / "libext_ledger.dylib"
        ledger.write_bytes(b"changed")
        self.call("verify", self.staged, "darwin/arm64", self.root / "changed-ledger", ok=False)
        ledger.unlink()
        self.call("verify", self.staged, "darwin/arm64", self.root / "missing-ledger", ok=False)

    def test_default_release_requires_every_platform(self):
        env = {key: value for key, value in os.environ.items() if key != "EXT_RELEASE_TARGETS"}
        env["EXT_RELEASE_NATIVE_DIR"] = str(self.root / "native")
        output = self.root / "dist"
        result = subprocess.run(["sh", str(ROOT / "scripts/build-release-ext.sh"), "test", str(output)],
                                env=env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list(output.glob("ext-*")), [])


if __name__ == "__main__":
    unittest.main()
