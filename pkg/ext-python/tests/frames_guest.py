"""The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
docs/plugin-guest-frames.md): reports progress and calls its host. It reads inline, so it
answers the operations that need a reader of its own (idle, collect, burst) with the public
error "unsupported"."""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, ".."))
import ext_plugin  # noqa: E402
from ext_plugin import FrameError, Progress, RemoteError  # noqa: E402

DESCRIPTOR = os.path.join(HERE, "..", "..", "..", "lib", "plugin", "testdata", "v1", "frames-descriptor.json")
HOST = ("host.svc", "v1")


def outcome(call):
    """How a reverse call came out, in the shape the suite reads."""
    try:
        return {"result": call()}
    except RemoteError as exc:
        error = {"code": exc.code, "message": exc.message}
        if exc.retry_after_ms:
            error["retry"] = exc.retry_after_ms
        return {"error": error}
    except FrameError as exc:
        return {"error": {"other": exc.code if exc.code in ("closed", "deadline", "canceled") else "failed"}}


def report(call, event):
    try:
        return call.progress(event)
    except FrameError:
        return None


def handler(contract, operation, payload):
    call = ext_plugin.current_call()
    payload = payload if isinstance(payload, dict) else {}
    if operation == "stream":
        report(call, Progress.fraction(1, 3))
        report(call, Progress.out("hello"))
        report(call, Progress.err("oops"))
        return {"done": True}
    if operation == "flood":
        dropped = 0
        for _ in range(payload.get("count", 300)):
            if report(call, Progress.log("x")) == ext_plugin.DROPPED:
                dropped += 1
        return {"dropped": dropped}
    if operation == "limits":
        limits = call.progress_limits
        return {
            "declared": limits is not None,
            "events": limits.events_per_second if limits else 0,
            "bytes": limits.bytes_per_second if limits else 0,
        }
    if operation == "split":
        pieces = ext_plugin.split_progress_text(payload["text"])
        for piece in pieces:
            report(call, Progress.out(piece))
        return {"pieces": len(pieces)}
    if operation == "plain":
        try:
            call.progress(Progress.log("never sent"))
            unsupported = False
        except FrameError as exc:
            unsupported = exc.code == "unsupported"
        return {"unsupported": unsupported, "limits": call.progress_limits is not None}
    if operation == "call":
        if call.reverse is None:
            return {"error": {"other": "unsupported"}}
        return outcome(lambda: call.reverse.call(HOST, payload["op"], payload.get("payload")))
    if operation == "twice":
        try:
            first = call.reverse.call(HOST, "sign", 1)
            second = call.reverse.call(HOST, "sign", 2)
        except (RemoteError, FrameError) as exc:
            raise RuntimeError("a reverse call failed: %s" % exc)
        return {"first": first, "second": second}
    if operation in ("idle", "collect", "burst"):
        raise RemoteError("unsupported", "not supported here")
    raise RuntimeError("unknown operation")


if __name__ == "__main__":
    ext_plugin.serve_stdio(DESCRIPTOR, handler, redact_errors=True)
