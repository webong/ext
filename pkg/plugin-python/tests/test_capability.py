import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, ".."))
import ext_plugin  # noqa: E402
from ext_plugin import capability as cap  # noqa: E402

IDENT = {"id": "example/p", "revision": "r1"}
DESC = {
    "apiVersion": "ext.plugin/v1",
    "identity": IDENT,
    "contracts": [
        {"name": "ext.health", "version": "v1", "operations": [{"name": "check", "surface": "observation"}]},
        {"name": "ext.configuration", "version": "v1", "operations": [{"name": "update", "surface": "configuration"}]},
        {"name": "ext.stream", "version": "v1", "operations": [{"name": "open"}, {"name": "read"}, {"name": "close"}]},
    ],
}


def request(contract, operation, payload, surface=""):
    r = {
        "apiVersion": "ext.plugin/v1",
        "id": "c1",
        "plugin": IDENT,
        "contract": {"name": contract, "version": "v1"},
        "operation": operation,
        "deadline": "2999-01-01T00:00:00Z",
        "payload": payload,
    }
    if surface:
        r["surface"] = surface
    return r


class CapabilityTest(unittest.TestCase):
    def setUp(self):
        self.updates = []
        self.closed = []
        self.router = cap.Router()
        self.router.health(lambda: {"status": "degraded", "ready": False})
        self.router.configuration(lambda i, r, v: self.updates.append((i, r, v)))
        self.router.stream(
            lambda parameters: "s1",
            lambda id_, seq, limit: {"items": [{"v": seq}] * min(limit, 2), "done": seq >= 2},
            lambda id_: self.closed.append(id_),
        )
        self.guest = ext_plugin.Guest(DESC, self.router)

    def call(self, contract, operation, payload, surface=""):
        return self.guest.invoke(request(contract, operation, payload, surface))

    def test_health(self):
        r = self.call("ext.health", "check", {}, "observation")
        self.assertEqual(r["payload"], {"status": "degraded", "ready": False})
        r = self.call("ext.health", "check", {"x": 1}, "observation")
        self.assertEqual(r["error"]["code"], "invalid_request")

    def test_health_result_is_validated(self):
        bad = cap.Router().health(lambda: {"status": "great", "ready": True})
        r = ext_plugin.Guest(DESC, bad).invoke(request("ext.health", "check", {}, "observation"))
        self.assertIn("error", r)
        self.assertNotEqual(r["error"]["code"], "invalid_request")

    def test_configuration(self):
        ok = {"instance": "one", "revision": "r1", "values": {"name": "a"}}
        r = self.call("ext.configuration", "update", ok, "configuration")
        self.assertEqual(r["payload"], {"instance": "one", "revision": "r1"})
        self.assertEqual(self.updates, [("one", "r1", {"name": "a"})])
        for bad in [
            dict(ok, instance=""),
            dict(ok, revision=""),
            dict(ok, instance="x" * 257),
            dict(ok, revision="r" * 257),
            {"instance": "one", "revision": "r1"},
            dict(ok, extra=1),
            dict(ok, instance=1),
        ]:
            r = self.call("ext.configuration", "update", bad, "configuration")
            self.assertEqual(r["error"]["code"], "invalid_request", bad)
        self.assertEqual(len(self.updates), 1)
        long_ok = dict(ok, instance="x" * 256, revision="r" * 256)
        self.assertEqual(self.call("ext.configuration", "update", long_ok, "configuration")["payload"]["instance"], "x" * 256)

    def test_stream(self):
        self.assertEqual(self.call("ext.stream", "open", {"parameters": {}})["payload"], {"id": "s1"})
        r = self.call("ext.stream", "read", {"id": "s1", "sequence": 1, "limit": 5})
        self.assertEqual(r["payload"], {"items": [{"v": 1}, {"v": 1}], "done": False})
        self.assertEqual(self.call("ext.stream", "read", {"id": "s1", "sequence": 2, "limit": 1})["payload"]["done"], True)
        for bad in [
            {"id": "", "sequence": 1, "limit": 1},
            {"id": "i" * 65, "sequence": 1, "limit": 1},
            {"id": "s1", "sequence": 0, "limit": 1},
            {"id": "s1", "sequence": 1, "limit": 0},
            {"id": "s1", "sequence": 1, "limit": -1},
            {"id": "s1", "sequence": 1, "limit": cap.MAX_STREAM_ITEMS + 1},
            {"id": "s1", "sequence": True, "limit": 1},
            {"id": "s1", "sequence": 1.5, "limit": 1},
        ]:
            r = self.call("ext.stream", "read", bad)
            self.assertEqual(r["error"]["code"], "invalid_request", bad)
        self.assertEqual(self.call("ext.stream", "close", {"id": "s1"})["payload"], {})
        self.assertEqual(self.closed, ["s1"])
        self.assertEqual(self.call("ext.stream", "close", {"id": ""})["error"]["code"], "invalid_request")

    def test_batch_limits(self):
        cap.stream_batch({"items": [{"v": i} for i in range(cap.MAX_STREAM_ITEMS)], "done": False})
        with self.assertRaises(ValueError):
            cap.stream_batch({"items": [{"v": i} for i in range(cap.MAX_STREAM_ITEMS + 1)], "done": False})
        big = "x" * (600 << 10)
        with self.assertRaises(ValueError):
            cap.stream_batch({"items": [big, big], "done": False})
        with self.assertRaises(ValueError):
            cap.stream_batch({"items": [], "done": 1})

    def test_unregistered_operation_and_duplicates(self):
        router = cap.Router()
        with self.assertRaises(ext_plugin.RemoteError):
            router("ext.health", "check", {})
        router.health(lambda: {"status": "healthy", "ready": True})
        with self.assertRaises(ValueError):
            router.health(lambda: {"status": "healthy", "ready": True})


if __name__ == "__main__":
    unittest.main()
