"""The inline router behind ``serve``: progress events, reverse calls and the rules a host that
breaks them meets. The host is a test thread on pipes."""
import json
import os
import queue
import sys
import threading
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, ".."))
import ext_plugin  # noqa: E402
from ext_plugin import frames  # noqa: E402

IDENT = {"id": "py/frames", "revision": "r1"}
CONTRACT = ("test.frames", "v1")
HOST = ("host.svc", "v1")
FUTURE = "2999-01-01T00:00:00Z"
DESC = {
    "apiVersion": "ext.plugin/v1",
    "identity": IDENT,
    "contracts": [
        {
            "name": "test.frames",
            "version": "v1",
            "operations": [
                {"name": "events", "progress": True},
                {"name": "flood", "progress": True},
                {"name": "invalid", "progress": True},
                {"name": "quiet"},
                {"name": "ask"},
                {"name": "session"},
                {"name": "thread"},
                {"name": "stale"},
            ],
        }
    ],
    "reverse": [{"name": "host.svc", "version": "v1", "operations": [{"name": "sign"}]}],
}

kept = []


def handler(contract, operation, payload):
    call = ext_plugin.current_call()
    if operation == "events":
        first = call.progress(ext_plugin.Progress.log("a"))
        second = call.progress(ext_plugin.Progress.fraction(1, 2))
        return {"sent": [first == ext_plugin.SENT, second == ext_plugin.SENT]}
    if operation == "flood":
        dropped = 0
        for _ in range(300):
            if call.progress(ext_plugin.Progress.log("x")) == ext_plugin.DROPPED:
                dropped += 1
        return {"dropped": dropped}
    if operation == "quiet":
        try:
            call.progress(ext_plugin.Progress.log("x"))
        except ext_plugin.FrameError as exc:
            return {"unsupported": exc.code == "unsupported"}
        return {"unsupported": False}
    if operation == "invalid":
        codes = []
        for event in (
            ext_plugin.Progress("mystery"),
            ext_plugin.Progress.fraction(3, 2),
            ext_plugin.Progress.log("x" * 2049),
            ext_plugin.Progress("log", "both"),
            ext_plugin.Progress.log("\ud800"),
            ext_plugin.Progress("progress", "out"),
            ext_plugin.Progress.fraction(-1, 0),
            ext_plugin.Progress.fraction(True, 2),
            None,
        ):
            try:
                call.progress(event)
                codes.append("sent")
            except ext_plugin.FrameError as exc:
                codes.append(exc.code)
        return {"codes": codes}
    if operation == "ask":
        try:
            return {"result": call.reverse.call(HOST, payload.get("op", "sign"), payload.get("payload"))}
        except ext_plugin.RemoteError as exc:
            return {"code": exc.code}
        except ext_plugin.FrameError as exc:
            return {"frame": exc.code}
    if operation == "session":
        return {"session": call.session_reverse is not None, "reverse": call.reverse is not None,
                "concurrent": bool(call.reverse and call.reverse.concurrent)}
    if operation == "thread":
        out = []
        worker = threading.Thread(target=lambda: out.append(_attempt(call.reverse)))
        worker.start()
        worker.join()
        return {"codes": out}
    if operation == "stale":
        kept.append(call.reverse)
        return None
    raise AssertionError(operation)


def _attempt(caller):
    try:
        caller.call(HOST, "sign", None)
        return "ok"
    except ext_plugin.FrameError as exc:
        return exc.code
    except ext_plugin.RemoteError as exc:
        return exc.code


def hello(**kw):
    r = {"apiVersion": "ext.plugin/v1", "id": "hello", "operation": "plugin.hello", "deadline": FUTURE}
    r.update(kw)
    return r


def answer(request_id, **body):
    r = {"frame": "reverse_response", "apiVersion": "ext.plugin/v1", "id": request_id}
    r.update(body)
    return r


class Host:
    """Serves a guest on pipes and reads what it writes."""

    def __init__(self, first=True):
        self.guest = ext_plugin.Guest(DESC, handler, redact_errors=True)
        to_read, to_write = os.pipe()
        from_read, from_write = os.pipe()
        self._to = os.fdopen(to_write, "wb", buffering=0)
        self._from = os.fdopen(from_read, "rb")
        self.frames = queue.Queue()
        self.outcome = None
        self.next = 0

        def serve():
            guest_in = os.fdopen(to_read, "rb")
            guest_out = os.fdopen(from_write, "wb", buffering=0)
            try:
                ext_plugin.serve(self.guest, guest_in, guest_out)
            except BaseException as exc:  # noqa: BLE001 - the test reads what ended the session
                self.outcome = exc
            finally:
                guest_out.close()
                guest_in.close()

        def read():
            for line in self._from:
                self.frames.put(json.loads(line))

        self.served = threading.Thread(target=serve, daemon=True)
        self.reader = threading.Thread(target=read, daemon=True)
        self.served.start()
        self.reader.start()
        if first:
            self.send(hello())

    def send(self, frame):
        self._to.write(json.dumps(frame).encode() + b"\n")

    def recv(self, wait=5.0):
        """The next frame the guest wrote, or None when none comes within ``wait`` seconds."""
        try:
            return self.frames.get(timeout=wait)
        except queue.Empty:
            return None

    def request(self, operation, payload=None, progress=False):
        self.next += 1
        request_id = str(self.next)
        r = {
            "apiVersion": "ext.plugin/v1",
            "id": request_id,
            "plugin": IDENT,
            "contract": {"name": CONTRACT[0], "version": CONTRACT[1]},
            "operation": operation,
            "payload": payload,
            "deadline": FUTURE,
        }
        if progress:
            r["progress"] = {"eventsPerSecond": 5}
        self.send(r)
        return request_id

    def until_response(self, request_id):
        """Reads frames up to the response to ``request_id``: those before it, and it."""
        before = []
        while True:
            frame = self.recv()
            assert frame is not None, "the guest wrote nothing"
            if "frame" not in frame and frame["id"] == request_id:
                return before, frame
            before.append(frame)

    def finish(self):
        """Ends the host's side and waits for the guest to end; what ended the session, or None."""
        self._to.close()
        self.served.join(10)
        assert not self.served.is_alive(), "the guest did not end"
        self.reader.join(10)  # the guest closed its end, so the reader reaches the end of the stream
        self._from.close()
        return self.outcome


def started():
    host = Host()
    assert host.recv()["id"] == "hello"
    return host


class ServeFramesTest(unittest.TestCase):
    def test_the_descriptor_declares_progress_and_reverse(self):
        host = Host()
        served = host.recv()["payload"]
        ops = {op["name"]: op for op in served["contracts"][0]["operations"]}
        self.assertIs(ops["events"]["progress"], True)
        self.assertNotIn("progress", ops["quiet"])
        self.assertEqual(served["reverse"][0]["name"], "host.svc")
        self.assertIsNone(host.finish())

    def test_events_come_in_order_before_the_response(self):
        host = started()
        request_id = host.request("events", progress=True)
        events, response = host.until_response(request_id)
        self.assertEqual(len(events), 2, events)
        for event in events:
            parent, _ = frames.validate_progress_frame(json.loads(json.dumps(event)))
            self.assertEqual(parent, request_id)
        self.assertEqual((events[0]["kind"], events[0]["text"]), ("log", "a"))
        self.assertEqual((events[1]["kind"], events[1]["completed"], events[1]["total"]), ("progress", 1, 2))
        self.assertEqual(response["payload"], {"sent": [True, True]})
        self.assertIsNone(host.recv(0.1), "a frame after the response")
        self.assertIsNone(host.finish())

    def test_a_flood_is_limited_counted_and_closed_with_a_marker(self):
        host = started()
        request_id = host.request("flood", progress=True)
        events, response = host.until_response(request_id)
        dropped = response["payload"]["dropped"]
        delivered = sum(1 for e in events if "text" in e)
        reported = sum(e.get("dropped", 0) for e in events)
        self.assertTrue(delivered >= 10 and delivered + dropped == 300 and reported == dropped, (delivered, dropped, reported))
        # The drops still unreported when the handler returned are in a marker with only a kind and a count.
        last = events[-1]
        self.assertTrue("text" not in last and last["kind"] == "log" and last["dropped"] > 0, last)
        self.assertIsNone(host.finish())

    def test_an_operation_that_did_not_declare_progress_cannot_report(self):
        host = started()
        for progress in (False, True):  # a rate for an operation that declared none is not a reporter either
            request_id = host.request("quiet", progress=progress)
            events, response = host.until_response(request_id)
            self.assertEqual(events, [])
            self.assertIs(response["payload"]["unsupported"], True)
        # A declared operation the host asked no events of cannot report.
        request_id = host.request("events", progress=False)
        events, response = host.until_response(request_id)
        self.assertEqual(events, [])
        self.assertEqual(response["error"]["code"], "operation_failed")
        self.assertIsNone(host.finish())

    def test_an_event_that_breaks_the_rules_is_refused_and_never_sent(self):
        host = started()
        request_id = host.request("invalid", progress=True)
        events, response = host.until_response(request_id)
        self.assertEqual(events, [])
        self.assertEqual(response["payload"]["codes"], ["invalid"] * 9)
        self.assertIsNone(host.finish())

    def test_a_reverse_call_names_the_call_and_returns_the_answer(self):
        host = started()
        request_id = host.request("ask", {"op": "sign", "payload": {"n": 1}})
        request = host.recv()
        frames.validate_reverse_request(request)
        self.assertEqual((request["frame"], request["id"], request["parent"], request["operation"]), ("reverse_request", "g1", request_id, "sign"))
        self.assertEqual(request["contract"], {"name": "host.svc", "version": "v1"})
        self.assertEqual(request["payload"], {"n": 1})
        host.send(answer("g1", payload="signed"))
        self.assertEqual(host.until_response(request_id)[1]["payload"], {"result": "signed"})
        # The host's error comes back with its code, and ids go on counting.
        request_id = host.request("ask", {"op": "sign"})
        request = host.recv()
        self.assertEqual(request["id"], "g2")
        host.send(answer("g2", error={"code": "denied", "message": "reverse call refused"}))
        self.assertEqual(host.until_response(request_id)[1]["payload"], {"code": "denied"})
        # An operation the descriptor does not list is refused without a frame.
        request_id = host.request("ask", {"op": "nothere"})
        before, response = host.until_response(request_id)
        self.assertEqual(before, [])
        self.assertEqual(response["payload"], {"code": "reverse_undeclared"})
        self.assertIsNone(host.finish())

    def test_only_one_reverse_call_at_a_time_and_no_session_caller(self):
        host = started()
        request_id = host.request("session")
        _, response = host.until_response(request_id)
        self.assertEqual(response["payload"], {"session": False, "reverse": True, "concurrent": False})
        # A thread other than the handler's cannot read the connection.
        request_id = host.request("thread")
        before, response = host.until_response(request_id)
        self.assertEqual((before, response["payload"]), ([], {"codes": ["unsupported"]}))
        self.assertIsNone(host.finish())

    def test_a_caller_ends_with_its_call(self):
        host = started()
        request_id = host.request("stale")
        host.until_response(request_id)
        # A request names the call it belongs to, so a caller kept past the call writes nothing.
        with self.assertRaises(ext_plugin.FrameError) as raised:
            kept[-1].call(HOST, "sign", None)
        self.assertEqual(raised.exception.code, "closed")
        self.assertIsNone(host.recv(0.1))
        self.assertIsNone(host.finish())

    def test_what_the_host_may_not_do_ends_the_session(self):
        violations = [
            ("a second request during a call", None),
            ("an unknown frame", {"frame": "mystery"}),
            ("an answer to a request never issued", answer("g99", payload=1)),
            ("a progress frame from the host", {"frame": "progress", "apiVersion": "ext.plugin/v1", "parent": "1", "kind": "log"}),
            ("a malformed answer to the request waiting", answer("g1", payload=1, extra=1)),
            ("an answer with both payload and error", answer("g1", payload=1, error={"code": "x", "message": "m"})),
            ("an answer with neither payload nor error", answer("g1")),
            ("a frame that is not an object", 7),
        ]
        for what, frame in violations:
            host = started()
            request_id = host.request("ask", {"op": "sign"})
            self.assertEqual(host.recv()["id"], "g1")
            if frame is None:
                host.request("events", progress=True)
            else:
                host.send(frame)
            failure = host.finish()
            self.assertIsInstance(failure, ext_plugin.ProtocolError, what)
            # The call that was in flight is never answered.
            while True:
                left = host.recv(0.05)
                if left is None:
                    break
                self.assertTrue("frame" in left or left["id"] != request_id, "%s: answered %s" % (what, left))

    def test_a_connection_that_ends_releases_a_waiting_handler(self):
        host = started()
        request_id = host.request("ask", {"op": "sign"})
        self.assertEqual(host.recv()["id"], "g1")
        # The host closes its end: the handler waiting for the answer is released, and the call is
        # still answered (the host may be reading), then the session ends.
        finishing = threading.Thread(target=host.finish)
        finishing.start()
        _, response = host.until_response(request_id)
        self.assertEqual(response["payload"], {"frame": "closed"})
        finishing.join(10)
        self.assertIsNone(host.outcome)

    def test_a_guest_invoked_directly_has_no_frames(self):
        guest = ext_plugin.Guest(DESC, handler, redact_errors=True)

        def request(operation):
            return {
                "apiVersion": "ext.plugin/v1", "id": "1", "plugin": IDENT,
                "contract": {"name": "test.frames", "version": "v1"}, "operation": operation,
                "payload": {}, "deadline": FUTURE, "progress": {},
            }

        self.assertEqual(guest.invoke(request("events"))["error"]["code"], "operation_failed")  # needs a reporter
        self.assertEqual(guest.invoke(request("ask"))["error"]["code"], "operation_failed")  # needs a reverse caller
        self.assertEqual(guest.invoke(request("session"))["payload"], {"session": False, "reverse": False, "concurrent": False})

    def test_hello_may_not_carry_a_rate_or_a_parent(self):
        for extra in ({"progress": {}}, {"parent": "1"}):
            host = Host(first=False)
            host.send(hello(**extra))
            self.assertIsInstance(host.finish(), ext_plugin.ProtocolError, extra)
            self.assertIsNone(host.recv(0.05))


class SplitTest(unittest.TestCase):
    def test_text_is_cut_on_characters(self):
        pieces = ext_plugin.split_progress_text("é" * 1500 + "\ud800")  # 3000 bytes, then a replacement
        self.assertEqual("".join(pieces), "é" * 1500 + "�")
        self.assertTrue(all(len(p.encode("utf-8")) <= 2048 for p in pieces))
        self.assertEqual(len(pieces), 2)
        self.assertEqual(ext_plugin.split_progress_text(""), [])


if __name__ == "__main__":
    unittest.main()
