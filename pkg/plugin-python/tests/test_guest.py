import io
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, ".."))
import ext_plugin  # noqa: E402
from ext_plugin import guest as g  # noqa: E402

IDENT = {"id": "example/p", "revision": "r1"}
DESC = {
    "apiVersion": "ext.plugin/v1",
    "identity": IDENT,
    "contracts": [
        {
            "name": "example.echo",
            "version": "v1",
            "operations": [{"name": "echo"}, {"name": "seen", "surface": "observation"}, {"name": "boom"}, {"name": "busy"}, {"name": "noisy"}],
        }
    ],
}
FUTURE = "2999-01-01T00:00:00Z"


def req(op="echo", **kw):
    r = {
        "apiVersion": "ext.plugin/v1",
        "id": "c1",
        "plugin": IDENT,
        "contract": {"name": "example.echo", "version": "v1"},
        "operation": op,
        "deadline": FUTURE,
        "payload": {"v": 1},
    }
    r.update(kw)
    return r


def handler(contract, op, payload):
    if op == "boom":
        raise RuntimeError("x" * 5000)
    if op == "busy":
        raise ext_plugin.RemoteError("busy", "later", retry_after_ms=250)
    if op == "noisy":
        print("stray output")
        return "ok"
    return payload


def dump(path, value):
    with open(path, "w") as f:
        json.dump(value, f)


def frames(*objs):
    return b"".join(json.dumps(o).encode() + b"\n" for o in objs)


def hello(**kw):
    r = {"apiVersion": "ext.plugin/v1", "id": "hello", "plugin": {"id": "", "revision": ""}, "contract": {"name": "", "version": ""}, "operation": "plugin.hello", "deadline": FUTURE}
    r.update(kw)
    return r


def run(stream_bytes, **kw):
    out = io.BytesIO()
    guest = ext_plugin.Guest(DESC, handler, **kw)
    err = None
    try:
        ext_plugin.serve(guest, io.BytesIO(stream_bytes), out)
    except ext_plugin.ProtocolError as exc:
        err = exc
    return [json.loads(line) for line in out.getvalue().splitlines()], err


class GuestTest(unittest.TestCase):
    def test_hello_and_echo(self):
        out, err = run(frames(hello(), req()))
        self.assertIsNone(err)
        self.assertEqual(out[0]["payload"], DESC)
        self.assertEqual(out[1], {"apiVersion": "ext.plugin/v1", "id": "c1", "payload": {"v": 1}})

    def test_hello_must_be_first_and_strict(self):
        for bad in (req(), hello(id="x"), hello(operation="echo"), hello(payload=None), hello(plugin=IDENT), hello(surface="s")):
            out, err = run(frames(bad))
            self.assertIsNotNone(err, bad)
            self.assertEqual(out, [])

    def test_empty_result_is_null(self):
        guest = ext_plugin.Guest(DESC, lambda c, o, p: None)
        self.assertEqual(guest.invoke(req())["payload"], None)
        self.assertIn("payload", guest.invoke(req()))

    def test_request_validation(self):
        cases = [
            req(apiVersion="ext.plugin/v2"),
            req(id="-bad"),
            req(plugin={"id": "example/p", "revision": "r2"}),
            req(contract={"name": "example.echo", "version": "v2"}),
            req(op="missing"),
            req(op="seen"),  # declared surface missing
            req(deadline="0001-01-01T00:00:00Z"),
            req(op="plugin.hello"),
        ]
        guest = ext_plugin.Guest(DESC, handler)
        for r in cases:
            resp = guest.invoke(r)
            self.assertEqual(resp["error"]["code"], "invalid_request", r)
            self.assertNotIn("payload", resp)
        self.assertEqual(guest.invoke(req(op="seen", surface="observation"))["payload"], {"v": 1})
        self.assertEqual(guest.invoke(req(op="seen", surface="other"))["error"]["code"], "invalid_request")

    def test_missing_deadline_is_invalid_request(self):
        r = req()
        del r["deadline"]
        self.assertEqual(ext_plugin.Guest(DESC, handler).invoke(r)["error"]["code"], "invalid_request")

    def test_expired_deadline(self):
        resp = ext_plugin.Guest(DESC, handler).invoke(req(deadline="2000-01-01T00:00:00Z"))
        self.assertEqual(resp["error"]["code"], "provider_error")

    def test_deadline_offsets_and_fractions(self):
        for stamp in ("2999-01-01T00:00:00.123456789Z", "2999-01-01T05:30:00+05:30", "2999-01-01T00:00:00.5-07:00"):
            self.assertEqual(ext_plugin.Guest(DESC, handler).invoke(req(deadline=stamp))["payload"], {"v": 1}, stamp)

    def test_errors(self):
        guest = ext_plugin.Guest(DESC, handler)
        busy = guest.invoke(req("busy"))["error"]
        self.assertEqual(busy, {"code": "busy", "message": "later", "retryAfterMilliseconds": 250})
        boom = guest.invoke(req("boom"))["error"]
        self.assertEqual(boom["code"], "provider_error")
        self.assertEqual(len(boom["message"]), 4096)
        redacted = ext_plugin.Guest(DESC, handler, redact_errors=True).invoke(req("boom"))["error"]
        self.assertEqual(redacted, {"code": "operation_failed", "message": "plugin operation failed"})
        multibyte = ext_plugin.Guest(DESC, lambda c, o, p: 1 / 0 if False else (_ for _ in ()).throw(RuntimeError("é" * 4000)))
        self.assertLessEqual(len(multibyte.invoke(req())["error"]["message"].encode()), 4096)

    def test_remote_error_validation(self):
        for args in (("Bad", "m"), ("ok", "x" * 4097), ("ok", "m", -1), ("ok", "m", g.MAX_RETRY_AFTER_MS + 1), ("ok", "m", True)):
            with self.assertRaises(ValueError):
                ext_plugin.RemoteError(*args)
        ext_plugin.RemoteError("ok", "m", g.MAX_RETRY_AFTER_MS)

    def test_unserializable_result_is_an_error(self):
        resp = ext_plugin.Guest(DESC, lambda c, o, p: object()).invoke(req())
        self.assertEqual(resp["error"]["code"], "provider_error")
        resp = ext_plugin.Guest(DESC, lambda c, o, p: float("nan")).invoke(req())
        self.assertEqual(resp["error"]["code"], "provider_error")

    def test_current_call(self):
        seen = {}

        def h(c, o, p):
            info = ext_plugin.current_call()
            seen.update(op=info.operation, id=info.request_id, ok=info.remaining() > 0)
            return None

        ext_plugin.Guest(DESC, h).invoke(req())
        self.assertEqual(seen, {"op": "echo", "id": "c1", "ok": True})
        self.assertIsNone(ext_plugin.current_call())

    def test_strict_json_ends_stream(self):
        bad = [
            b'{"apiVersion":"ext.plugin/v1","id":"hello","id":"hello"}\n',
            b'{"apiVersion":"ext.plugin/v1","unknown":1}\n',
            b'[1,2]\n',
            b'{"a":NaN}\n',
            b'{"a":"\\ud800"}\n',
            b'{"a":1} {"b":2}\n',
            b'\xff\xfe\n',
            b'{"a":' + b'[' * 70 + b']' * 70 + b'}\n',
        ]
        for raw in bad:
            out, err = run(raw)
            self.assertIsNotNone(err, raw)
            self.assertEqual(out, [])

    def test_payload_strictness_after_hello(self):
        out, err = run(frames(hello()) + b'{"apiVersion":"ext.plugin/v1","id":"c1","operation":"echo","deadline":"2999-01-01T00:00:00Z","payload":{"a":1,"a":2}}\n')
        self.assertIsNotNone(err)
        self.assertEqual(len(out), 1)

    def test_astral_pair_allowed(self):
        out, err = run(frames(hello()) + frames(req(payload="\U0001F600")))
        self.assertIsNone(err)
        self.assertEqual(out[1]["payload"], "\U0001F600")

    def test_frame_limit(self):
        limit = g.MAX_FRAME_BYTES
        out, err = run(b"x" * (limit + 1) + b"\n")
        self.assertIsNotNone(err)
        out, err = run(b"x" * (limit + 5))  # no newline, unbounded
        self.assertIsNotNone(err)
        out, err = run(frames(hello())[:-1])  # EOF inside a frame
        self.assertIsNotNone(err)

    def test_oversize_response_becomes_error(self):
        guest = ext_plugin.Guest(DESC, lambda c, o, p: "x" * g.MAX_FRAME_BYTES)
        out = io.BytesIO()
        ext_plugin.serve(guest, io.BytesIO(frames(hello(), req())), out)
        lines = out.getvalue().splitlines()
        self.assertEqual(json.loads(lines[1])["error"]["code"], "response_too_large")

    def test_clean_eof(self):
        self.assertEqual(run(b""), ([], None))

    def test_descriptor_loading(self):
        with tempfile.TemporaryDirectory() as d:
            bare = os.path.join(d, "descriptor.json")
            pkg = os.path.join(d, "package.json")
            dump(bare, DESC)
            dump(pkg, {"apiVersion": "ext.package/v1", "descriptor": DESC, "artifacts": []})
            self.assertEqual(ext_plugin.load_descriptor(bare), DESC)
            self.assertEqual(ext_plugin.load_descriptor(pkg), DESC)
            self.assertIsInstance(ext_plugin.Guest(pkg, handler), ext_plugin.Guest)
            dump(pkg, {"apiVersion": "other", "descriptor": DESC})
            with self.assertRaises(ValueError):
                ext_plugin.load_descriptor(pkg)

    def test_descriptor_validation(self):
        def mutate(f):
            d = json.loads(json.dumps(DESC))
            f(d)
            return d

        bad = [
            mutate(lambda d: d.update(apiVersion="x")),
            mutate(lambda d: d["identity"].update(id="Bad")),
            mutate(lambda d: d.update(contracts=[])),
            mutate(lambda d: d["contracts"][0].update(operations=[])),
            mutate(lambda d: d["contracts"][0]["operations"].append({"name": "plugin.hello"})),
            mutate(lambda d: d["contracts"][0]["operations"].append({"name": "echo"})),
            mutate(lambda d: d["contracts"].append(dict(d["contracts"][0]))),
            mutate(lambda d: d.update(extra=1)),
        ]
        for d in bad:
            with self.assertRaises(ValueError):
                ext_plugin.validate_descriptor(d)
        with self.assertRaises(ValueError):
            ext_plugin.Guest(bad[0], handler)


class StdioTest(unittest.TestCase):
    def test_stdout_carries_only_frames(self):
        script = (
            "import sys; sys.path.insert(0, %r); import ext_plugin\n"
            "def h(c, o, p):\n    print('noise on stdout'); import os; os.write(1, b'raw noise\\n'); return 'ok'\n"
            "ext_plugin.serve_stdio(%r, h)\n" % (os.path.join(HERE, ".."), DESC)
        )
        proc = subprocess.run([sys.executable, "-I", "-c", script], input=frames(hello(), req()), capture_output=True, timeout=30)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        lines = proc.stdout.splitlines()
        self.assertEqual(len(lines), 2, proc.stdout)
        self.assertEqual(json.loads(lines[1])["payload"], "ok")
        self.assertIn(b"noise on stdout", proc.stderr)


if __name__ == "__main__":
    unittest.main()
