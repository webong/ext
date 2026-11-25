"""Native package-writer integration; no guest process or compiler is required."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ext_io import host


class PackageWriterTest(unittest.TestCase):
    def setUp(self):
        library = os.environ.get("EXT_HOST_LIBRARY")
        if not library:
            if os.environ.get("EXT_HOST_REQUIRED") == "1":
                self.fail("EXT_HOST_LIBRARY is required")
            self.skipTest("set EXT_HOST_LIBRARY, or run package-acceptance.sh")
        self.engine = host.Engine(library)
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.home = Path(self.temporary.name)
        self.lock = self.home / ".package-write.lock"

    def assert_status(self, status, action):
        with self.assertRaises(host.HostError) as raised:
            action()
        self.assertEqual(raised.exception.status, status)

    def test_exclusion_and_exception_cleanup(self):
        with self.assertRaisesRegex(ValueError, "application failure"):
            with self.engine.package_writer(self.home):
                self.assert_status("exists", lambda: self.engine.package_writer(self.home))
                with self.assertRaises(FileExistsError):
                    self.lock.mkdir()  # Same primitive used by CLI/CTX.
                raise ValueError("application failure")
        self.assertFalse(self.lock.exists())
        writer = self.engine.package_writer(self.home)
        writer.close()
        writer.close()
        self.assert_status("closed", writer.__enter__)
        self.lock.mkdir()
        self.assert_status("exists", lambda: self.engine.package_writer(self.home))
        self.assertTrue(self.lock.is_dir())

    def test_release_preserves_replacement(self):
        writer = self.engine.package_writer(self.home)
        self.lock.rename(self.home / "old-writer")
        with self.engine.package_writer(self.home):
            self.assert_status("mismatch", writer.close)
            self.assertTrue(self.lock.is_dir())
        self.assertFalse(self.lock.exists())

    def test_process_loss_leaves_stale_lock(self):
        code = "from ext_io import host; import os,sys; w=host.Engine().package_writer(sys.argv[1]); os._exit(0)"
        env = dict(os.environ)
        env["PYTHONPATH"] = str(Path(__file__).resolve().parents[1])
        subprocess.run([sys.executable, "-c", code, str(self.home)], env=env, check=True)
        self.assert_status("exists", lambda: self.engine.package_writer(self.home))
        self.assertTrue(self.lock.is_dir())
