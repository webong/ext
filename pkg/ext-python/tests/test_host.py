"""The host SDK (ext_io.host) on the C engine.

It needs the engine's shared library (EXT_HOST_LIBRARY) and runs the shared host cases
(lib/plugin/testdata/v1/host-cases.json) against the reference guest: EXT_GO_GUEST, the
JSON-line Go guest, and EXT_GO_SHARED, the same guest as a C ABI library. Without them the
tests are skipped, or fail when EXT_HOST_REQUIRED=1. scripts/ext-python-host.sh builds all
three and runs this file."""
import json
import os
import sys
import threading
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, ".."))
from ext_io import plugin  # noqa: E402

FIXTURES = os.path.join(HERE, "..", "..", "..", "lib", "plugin", "testdata", "v1")
REQUIRED = os.environ.get("EXT_HOST_REQUIRED") == "1"


def need(name):
    value = os.environ.get(name)
    if not value:
        if REQUIRED:
            raise AssertionError("%s is required" % name)
        raise unittest.SkipTest("set %s, or run scripts/ext-python-host.sh" % name)
    return os.path.abspath(value)


def fixture(name):
    with open(os.path.join(FIXTURES, name), "rb") as f:
        return json.loads(f.read().decode("utf-8"))


def engine():
    need("EXT_HOST_LIBRARY")
    from ext_io import host

    return host, host.Engine()


class GuestImportTest(unittest.TestCase):
    def test_guest_never_loads_the_host(self):
        # The guest stays dependency free: importing it does not import the host.
        import subprocess

        code = "import sys; sys.path.insert(0, %r); from ext_io import plugin; print('ext_io.host' in sys.modules)" % os.path.join(HERE, "..")
        out = subprocess.run([sys.executable, "-c", code], capture_output=True, check=True, text=True)
        self.assertEqual(out.stdout.strip(), "False")

    def test_missing_library_is_reported(self):
        from ext_io import host

        with self.assertRaises(OSError):
            host.Engine("/does/not/exist/libext_host.so")


class HostCasesTest(unittest.TestCase):
    """Every case of host-cases.json, on both reference guests."""

    def test_host_cases(self):
        host, eng = engine()
        cases = fixture("host-cases.json")
        descriptor = fixture(cases["descriptor"])
        contract = cases["contract"]
        for guest in cases["guests"]:
            path = need("EXT_GO_GUEST" if guest == "jsonline" else "EXT_GO_SHARED")
            for case in cases["cases"]:
                with self.subTest(guest=guest, case=case["name"]):
                    self.run_case(host, eng, guest, path, descriptor, contract, case)

    def run_case(self, host, eng, guest, path, descriptor, contract, case):
        d = json.loads(json.dumps(descriptor))
        if case.get("revision"):
            d["identity"]["revision"] = case["revision"]
        verify = (lambda _: False) if case.get("verify") == "deny" else None
        authorize = (lambda _: False) if case.get("authorize") == "deny" else None
        if guest == "jsonline":
            h = eng.process(d, path, verify=verify, authorize=authorize)
        else:
            h = eng.shared_library(d, path, verify=verify, authorize=authorize)
        with h:
            want = case.get("start", "ok")
            try:
                h.start(5.0)
                got = "ok"
            except host.HostError as e:
                got = e.status
            self.assertEqual(got, want, "start")
            if got != "ok":
                return
            for i, step in enumerate(case.get("steps", [])):
                if step.get("close"):
                    h.close()
                elif "state" in step:
                    self.assertEqual(h.state, step["state"], "step %d" % i)
                else:
                    self.run_call(host, h, contract, i, step)

    def run_call(self, host, h, contract, i, step):
        outcome, value = "result", None
        try:
            value = h.call(contract, step["call"], step.get("payload"), step.get("timeoutMs", 3000) / 1000.0)
        except plugin.RemoteError as e:
            outcome, value = "error", e
        except host.HostError as e:
            outcome, value = "status", e.status
        where = "step %d (%s)" % (i, step["call"])
        if step.get("fails"):
            self.assertNotEqual(outcome, "result", where)
        elif "result" in step:
            self.assertEqual((outcome, value), ("result", step["result"]), where)
        elif "error" in step:
            self.assertEqual(outcome, "error", "%s: %r" % (where, value))
            want = step["error"]
            self.assertEqual(value.code, want["code"], where)
            self.assertEqual(value.message, want["message"], where)
            self.assertEqual(value.retry_after_ms or 0, want.get("retryAfterMilliseconds", 0), where)
        else:
            allowed = step["status"] if isinstance(step["status"], list) else [step["status"]]
            self.assertEqual(outcome, "status", "%s: %r" % (where, value))
            self.assertIn(value, allowed, where)


class HostTest(unittest.TestCase):
    """What the cases leave to each binding: threads, policies, envelopes and ownership."""

    def setUp(self):
        self.host, self.eng = engine()
        self.guest = need("EXT_GO_GUEST")
        self.descriptor = fixture("descriptor.json")
        self.contract = self.descriptor["contracts"][0]

    def test_policies_see_json(self):
        seen = {}

        def verify(d):
            seen["verify"] = d["identity"]["id"]
            return True

        def authorize(r):
            seen["authorize"] = (r["operation"], r["payload"])
            return True

        with self.eng.process(self.descriptor, self.guest, verify=verify, authorize=authorize) as h:
            h.start()
            self.assertEqual(h.call(self.contract, "echo", {"value": 1}), {"value": 1})
        self.assertEqual(seen, {"verify": "ctx/conformance", "authorize": ("echo", {"value": 1})})

    def test_a_raising_policy_denies(self):
        def authorize(_):
            raise RuntimeError("policy bug")

        with self.eng.process(self.descriptor, self.guest, authorize=authorize) as h:
            h.start()
            with self.assertRaises(self.host.HostError) as e:
                h.call(self.contract, "echo", {"value": 1})
            self.assertEqual(e.exception.status, "denied")
            self.assertEqual(h.state, "ready")

    def test_calls_from_threads(self):
        with self.eng.process(self.descriptor, self.guest) as h:
            h.start()
            results, errors = [], []

            def work(n):
                try:
                    results.append(h.call(self.contract, "echo", {"value": n}))
                except Exception as e:  # noqa: BLE001
                    errors.append(e)

            threads = [threading.Thread(target=work, args=(n,)) for n in range(8)]
            for t in threads:
                t.start()
            for t in threads:
                t.join()
            self.assertEqual(errors, [])
            self.assertEqual(sorted(r["value"] for r in results), list(range(8)))

    def test_invoke_takes_envelopes(self):
        with self.eng.process(self.descriptor, self.guest) as h:
            h.start()
            request = {
                "apiVersion": "ext.plugin/v1", "id": "r1", "plugin": self.descriptor["identity"],
                "contract": {"name": self.contract["name"], "version": self.contract["version"]},
                "operation": "echo", "deadline": "2099-01-01T00:00:00Z", "payload": {"value": 3},
            }
            response = json.loads(h.invoke(json.dumps(request).encode()))
            self.assertEqual((response["id"], response["payload"]), ("r1", {"value": 3}))

    def test_relative_paths_are_refused(self):
        with self.assertRaises(self.host.HostError) as e:
            self.eng.shared_library(self.descriptor, "relative/libguest.so")
        self.assertEqual(e.exception.status, "invalid")
        with self.assertRaises(self.host.HostError) as e:
            self.eng.process(self.descriptor, "relative/guest")
        self.assertEqual(e.exception.status, "invalid")

    def test_destroyed_host_refuses_use(self):
        h = self.eng.process(self.descriptor, self.guest)
        h.destroy()
        h.destroy()  # once is enough; again is harmless
        with self.assertRaises(self.host.HostError) as e:
            h.start()
        self.assertEqual(e.exception.status, "closed")

    def test_stderr_is_kept(self):
        with self.eng.process(self.descriptor, self.guest) as h:
            h.start()
            self.assertIsInstance(h.stderr, bytes)


if __name__ == "__main__":
    unittest.main()
