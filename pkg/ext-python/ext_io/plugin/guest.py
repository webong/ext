import json
import os
import sys
import threading
import time

from . import frames
from .frames import FrameError
from ._core import (  # noqa: F401 - re-exported for callers and tests
    API_VERSION,
    MAX_FRAME_BYTES,
    MAX_MESSAGE_BYTES,
    MAX_RETRY_AFTER_MS,
    ProtocolError,
    RemoteError,
    _IDENTIFIER,
    _MAX_DEPTH,
    _REVISION,
    _TIME,
    _ZERO_TIME,
    _check_value,
    _constant,
    _has_surrogate,
    _match,
    _pairs,
    _parse_deadline,
    _read_frame,
    decode_frame,
    encode_frame,
)

DEFAULT_MAX_CALL_SECONDS = 30.0


class CallInfo:
    """Details of the call being handled, for handlers that need them.

    On a transport that carries frames (a guest served by :func:`serve`) the call can also
    report progress and call its host: see :meth:`progress`, ``progress_limits`` and
    ``reverse``. This guest reads inline, so ``session_reverse`` is always None and a handler
    makes one reverse call at a time, from its own thread.
    """

    __slots__ = (
        "request_id",
        "contract_name",
        "contract_version",
        "operation",
        "surface",
        "deadline",
        "progress_limits",
        "reverse",
        "session_reverse",
        "_reporter",
    )

    def __init__(self, request_id, contract_name, contract_version, operation, surface, deadline):
        self.request_id = request_id
        self.contract_name = contract_name
        self.contract_version = contract_version
        self.operation = operation
        self.surface = surface
        self.deadline = deadline  # seconds since the epoch (time.time() scale)
        #: The rate the host set for this call's events (a frames.ProgressLimits), or None when
        #: the operation reports none.
        self.progress_limits = None
        #: A frames.ReverseCaller for host operations, bound by the call's deadline, or None on a
        #: transport with no frames.
        self.reverse = None
        #: Always None here: nobody reads the connection between calls.
        self.session_reverse = None
        self._reporter = None

    def remaining(self):
        return self.deadline - time.time()

    def progress(self, event):
        """Reports a frames.Progress event to the host while the call runs. Events reach the host
        in order, before the result. Returns ``frames.SENT``, or ``frames.DROPPED`` when the rate
        limit dropped it (routine: the next event that passes carries the count). Raises
        FrameError: ``unsupported`` when the operation did not declare progress, the host asked
        for no events or there is no frame channel (stop reporting), ``invalid`` for an event that
        breaks the rules (nothing is sent), ``closed`` once the call is over."""
        if self._reporter is None:
            raise FrameError("unsupported", "no progress channel on this call")
        return self._reporter.report(event)


_current = None


def current_call():
    """The CallInfo of the in-flight call, or None outside a handler."""
    return _current


# --- descriptor -------------------------------------------------------------


def validate_descriptor(d):
    """Raise ValueError unless d is a valid ext.plugin/v1 descriptor."""
    if not isinstance(d, dict) or set(d) - {"apiVersion", "identity", "contracts", "reverse"}:
        raise ValueError("descriptor must be an object with apiVersion, identity, contracts")
    if d.get("apiVersion") != API_VERSION:
        raise ValueError("unsupported apiVersion")
    ident = d.get("identity")
    if not isinstance(ident, dict) or set(ident) - {"id", "revision", "version"}:
        raise ValueError("invalid identity")
    if not _match(_IDENTIFIER, ident.get("id")) or not _match(_REVISION, ident.get("revision")):
        raise ValueError("identity requires a bounded id and immutable revision")
    if "version" in ident and ident["version"] != "" and not _match(_REVISION, ident["version"]):
        raise ValueError("invalid identity version")
    contracts = d.get("contracts")
    if not isinstance(contracts, list) or not 1 <= len(contracts) <= 64:
        raise ValueError("declare 1..64 contracts")
    _validate_contracts(contracts, reverse=False)
    # The host operations the plugin may call; none is a null or empty list.
    reverse = d.get("reverse")
    if reverse is not None:
        if not isinstance(reverse, list) or len(reverse) > 64:
            raise ValueError("declare at most 64 reverse contracts")
        _validate_contracts(reverse, reverse=True)


def _validate_contracts(contracts, reverse):
    seen = set()
    for c in contracts:
        if not isinstance(c, dict) or set(c) - {"name", "version", "operations"}:
            raise ValueError("invalid contract")
        if not _match(_IDENTIFIER, c.get("name")) or not _match(_REVISION, c.get("version")):
            raise ValueError("contract requires a name and exact version")
        key = (c["name"], c["version"])
        ops = c.get("operations")
        if key in seen or not isinstance(ops, list) or not 1 <= len(ops) <= 256:
            raise ValueError("duplicate contract or invalid operation count")
        seen.add(key)
        names = set()
        for op in ops:
            if not isinstance(op, dict) or set(op) - {"name", "surface", "progress"}:
                raise ValueError("invalid operation")
            name, surface, progress = op.get("name"), op.get("surface"), op.get("progress")
            # Go leaves a member that is null alone, as if absent.
            surface = "" if surface is None else surface
            if (
                not _match(_IDENTIFIER, name)
                or name == "plugin.hello"
                or name in names
                or not isinstance(surface, str)
                or (surface != "" and not _match(_IDENTIFIER, surface))
                or (progress is not None and not isinstance(progress, bool))
                or (progress is True and reverse)  # a host operation does not report progress
            ):
                raise ValueError("invalid, reserved, or duplicate operation")
            names.add(name)


def _match(pattern, value):
    return isinstance(value, str) and pattern.match(value) is not None


def load_descriptor(path):
    """Read a descriptor from a plugin.json (ext.package/v1) or a bare descriptor file."""
    with open(os.fspath(path), "rb") as f:
        raw = f.read((16 << 20) + 1)
    if len(raw) > 16 << 20:
        raise ValueError("descriptor file exceeds 16 MiB")
    try:
        data = decode_frame(raw)
    except ProtocolError as exc:
        raise ValueError(str(exc))
    if isinstance(data, dict) and "descriptor" in data:
        if data.get("apiVersion") != "ext.package/v1":
            raise ValueError("unsupported package apiVersion")
        data = data["descriptor"]
    validate_descriptor(data)
    return data


# --- guest ------------------------------------------------------------------

_HELLO_FIELDS = {"apiVersion", "id", "plugin", "contract", "operation", "surface", "deadline", "payload"}
_REQUEST_FIELDS = _HELLO_FIELDS | {"progress", "parent"}


def _rate_of(value):
    """The rate a request sets for a call's progress events: None when absent, else
    ``(events_per_second, bytes_per_second)`` with 0 for a member left out. Go decodes the members
    into int64s, so a number that is not a whole one that fits is not a request at all."""
    if value is None:
        return None
    if not isinstance(value, dict) or set(value) - {"eventsPerSecond", "bytesPerSecond"}:
        raise ProtocolError("invalid progress rate")
    out = []
    for key in ("eventsPerSecond", "bytesPerSecond"):
        n = value.get(key)
        if n is None:
            n = 0
        if type(n) is not int or not -(1 << 63) <= n < 1 << 63:
            raise ProtocolError("invalid progress rate")
        out.append(n)
    return tuple(out)


def _identity_of(obj, fields):
    if obj is None:
        return {f: "" for f in fields}
    if not isinstance(obj, dict) or set(obj) - set(fields):
        raise ProtocolError("invalid envelope object")
    out = {}
    for f in fields:
        v = obj.get(f, "")
        if not isinstance(v, str):
            raise ProtocolError("invalid envelope field")
        out[f] = v
    return out


class Guest:
    """An immutable descriptor plus a domain handler.

    ``handler(contract_name, operation, payload)`` returns a JSON-serializable
    result, or raises RemoteError for a deliberate public failure. Any other
    exception becomes ``RemoteError("provider_error", str(exc)[:4096])``; pass
    ``redact_errors=True`` to send a generic ``operation_failed`` instead, so
    internal text never reaches the host. Calls are sequential.
    """

    def __init__(self, descriptor, handler, max_call_seconds=DEFAULT_MAX_CALL_SECONDS, redact_errors=False):
        if not isinstance(descriptor, dict):
            descriptor = load_descriptor(descriptor)
        else:
            validate_descriptor(descriptor)
        if not callable(handler):
            raise TypeError("handler is required")
        if max_call_seconds <= 0:
            raise ValueError("max_call_seconds must be positive")
        self._descriptor = json.loads(json.dumps(descriptor))
        self._handler = handler
        self._max_call = float(max_call_seconds)
        self._redact = bool(redact_errors)
        self._ops = {}  # (contract, version, operation) -> (surface, reports progress)
        for c in self._descriptor["contracts"]:
            for op in c["operations"]:
                self._ops[(c["name"], c["version"], op["name"])] = (op.get("surface") or "", op.get("progress") is True)
        self._reverse = set()  # (contract, version, operation) the plugin may call on its host
        for c in self._descriptor.get("reverse") or []:
            for op in c["operations"]:
                self._reverse.add((c["name"], c["version"], op["name"]))
        ident = self._descriptor["identity"]
        self._identity = {"id": ident["id"], "revision": ident["revision"], "version": ident.get("version", "")}

    def handshake(self):
        return json.loads(json.dumps(self._descriptor))

    def invoke(self, request, link=None):
        """Validate one decoded request and return the response object.

        Raises ProtocolError for envelopes the Go host would not even decode
        (unknown fields, wrong types); those end the stream. ``link`` is the connection the
        request came in on (:func:`serve` passes it): without one the call can neither report
        progress nor call its host.
        """
        global _current
        if not isinstance(request, dict) or set(request) - _REQUEST_FIELDS:
            raise ProtocolError("invalid request envelope")
        request_id = request.get("id")
        if not isinstance(request_id, str):
            raise ProtocolError("invalid request id")
        plugin = _identity_of(request.get("plugin"), ("id", "revision", "version"))
        contract = _identity_of(request.get("contract"), ("name", "version"))
        for k in ("apiVersion", "operation", "surface"):
            if k in request and not isinstance(request[k], str):
                raise ProtocolError("invalid request field")
        if "deadline" in request and not isinstance(request["deadline"], str):
            raise ProtocolError("invalid request deadline")
        if request.get("parent") is not None and not isinstance(request["parent"], str):
            raise ProtocolError("invalid request parent")
        rate = _rate_of(request.get("progress"))
        response = {"apiVersion": API_VERSION, "id": request_id}
        deadline = _parse_deadline(request.get("deadline"))
        operation = request.get("operation", "")
        surface = request.get("surface", "")
        key = (contract["name"], contract["version"], operation)
        if (
            request.get("apiVersion") != API_VERSION
            or not _match(_REVISION, request_id)
            or deadline is None
            or plugin != self._identity
            or key not in self._ops
            or self._ops[key][0] != surface
        ):
            response["error"] = {"code": "invalid_request", "message": "request does not match selected contract"}
            return response
        limit = min(deadline, time.time() + self._max_call)
        error = None
        result = None
        if time.time() >= limit:
            error = self._failure(TimeoutError("deadline exceeded"))
        else:
            info = _current = CallInfo(request_id, contract["name"], contract["version"], operation, surface, limit)
            reporter = caller = None
            if link is not None:
                # A call reports progress when its operation declared it and the host asked for
                # events; it can call the host on the connection it came in on.
                if rate is not None and self._ops[key][1]:
                    info.progress_limits = frames.ProgressLimits(
                        rate[0] if rate[0] > 0 else frames.DEFAULT_PROGRESS_EVENTS,
                        rate[1] if rate[1] > 0 else frames.DEFAULT_PROGRESS_BYTES,
                    )
                    reporter = info._reporter = frames.Reporter(link, request_id, info.progress_limits)
                caller = info.reverse = frames.ReverseCaller(link, self._reverse, request_id, limit)
            try:
                result = self._handler(contract["name"], operation, request.get("payload"))
                encode_frame(result)  # surface unserializable results as provider errors
            except RemoteError as exc:
                error = exc.to_wire()
            except Exception as exc:  # noqa: BLE001 - handler boundary
                error = self._failure(exc)
            else:
                if time.time() >= limit:
                    error = self._failure(TimeoutError("deadline exceeded"))
            finally:
                _current = None
                if caller is not None:
                    caller.over = True  # a request names its call, so a caller cannot outlive it
                if reporter is not None:
                    reporter.finish()  # a marker for events never reported, before the response
        if error is not None:
            response["error"] = error
        else:
            response["payload"] = result
        return response

    def _failure(self, exc):
        if self._redact:
            return {"code": "operation_failed", "message": "plugin operation failed"}
        text = str(exc).encode("utf-8", "replace")[:MAX_MESSAGE_BYTES].decode("utf-8", "ignore")
        return {"code": "provider_error", "message": text}


def serve(guest, stdin, stdout):
    """Serve ``guest`` over binary streams until EOF. Raises ProtocolError on violations.

    A handler can report progress and call its host (see :class:`CallInfo`). The host sends
    nothing during a call but the answer to a reverse call: anything else ends the session with
    ProtocolError, and the call in flight is not answered. When the input ends during a call, the
    call is answered and the session ends.
    """
    link = frames.Link(stdin, stdout)
    hello = False
    while True:
        raw = _read_frame(stdin)
        if raw is None:
            return
        request = decode_frame(raw)
        if not hello:
            response = _answer_hello(guest, request)
            hello = True
        else:
            response = guest.invoke(request, link)
            if link.failure is not None:
                raise link.failure  # the host broke the rules while the handler waited
        frame = encode_frame(response)
        if len(frame) > MAX_FRAME_BYTES:
            response = {
                "apiVersion": API_VERSION,
                "id": response["id"],
                "error": {"code": "response_too_large", "message": "response exceeds the frame limit"},
            }
            frame = encode_frame(response)
        link.write(frame)


def _answer_hello(guest, request):
    if not isinstance(request, dict) or set(request) - _HELLO_FIELDS:
        raise ProtocolError("invalid hello")
    plugin = _identity_of(request.get("plugin"), ("id", "revision", "version"))
    contract = _identity_of(request.get("contract"), ("name", "version"))
    deadline = _parse_deadline(request.get("deadline"))
    if (
        request.get("apiVersion") != API_VERSION
        or request.get("id") != "hello"
        or request.get("operation") != "plugin.hello"
        or any(plugin.values())
        or any(contract.values())
        or request.get("surface", "") != ""
        or "payload" in request
        or deadline is None
    ):
        raise ProtocolError("invalid hello")
    if time.time() >= deadline:
        raise ProtocolError("hello deadline exceeded")
    return {"apiVersion": API_VERSION, "id": "hello", "payload": guest.handshake()}


def serve_stdio(descriptor, handler, max_call_seconds=DEFAULT_MAX_CALL_SECONDS, redact_errors=False):
    """Serve on this process's stdin/stdout. Returns at EOF.

    descriptor is a dict or the path of a plugin.json / descriptor file.
    Protocol frames use a private duplicate of the original stdout; file
    descriptor 1 and ``sys.stdout`` are redirected to stderr, so stray prints
    cannot corrupt the stream. Call once, from the guest's main.
    """
    guest = Guest(descriptor, handler, max_call_seconds, redact_errors)
    out = os.fdopen(os.dup(1), "wb", buffering=0)
    sys.stdout.flush()
    os.dup2(2, 1)
    sys.stdout = sys.stderr
    try:
        serve(guest, sys.stdin.buffer, out)
    finally:
        out.close()
