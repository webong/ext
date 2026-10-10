import json
import os
import re
import sys
import time
from datetime import datetime, timedelta, timezone

API_VERSION = "ext.plugin/v1"
MAX_FRAME_BYTES = 24 << 20
MAX_RETRY_AFTER_MS = 365 * 24 * 60 * 60 * 1000
MAX_MESSAGE_BYTES = 4096
DEFAULT_MAX_CALL_SECONDS = 30.0
_MAX_DEPTH = 64

_IDENTIFIER = re.compile(r"\A[a-z0-9][a-z0-9._/-]{0,255}\Z")
_REVISION = re.compile(r"\A[A-Za-z0-9][A-Za-z0-9._:+/-]{0,255}\Z")
_TIME = re.compile(
    r"\A(\d{4})-(\d\d)-(\d\d)[Tt](\d\d):(\d\d):(\d\d)(\.\d+)?([Zz]|[+-]\d\d:\d\d)\Z"
)


class ProtocolError(Exception):
    """The peer violated the framing or envelope rules; the stream is unusable."""


class RemoteError(Exception):
    """A deliberate public failure sent to the host as the structured error.

    ``code`` must match the identifier syntax, ``message`` is at most 4096 UTF-8
    bytes, and ``retry_after_ms`` is an optional non-negative bound of one year.
    """

    def __init__(self, code, message, retry_after_ms=None):
        if not isinstance(code, str) or not _IDENTIFIER.match(code):
            raise ValueError("error code must match [a-z0-9][a-z0-9._/-]{0,255}")
        if not isinstance(message, str) or len(message.encode("utf-8", "replace")) > MAX_MESSAGE_BYTES:
            raise ValueError("error message must be a string of at most 4096 bytes")
        if retry_after_ms is not None and (
            isinstance(retry_after_ms, bool)
            or not isinstance(retry_after_ms, int)
            or retry_after_ms < 0
            or retry_after_ms > MAX_RETRY_AFTER_MS
        ):
            raise ValueError("retry_after_ms must be an integer in [0, one year]")
        super().__init__(message)
        self.code = code
        self.message = message
        self.retry_after_ms = retry_after_ms

    def to_wire(self):
        wire = {"code": self.code, "message": self.message}
        if self.retry_after_ms:
            wire["retryAfterMilliseconds"] = self.retry_after_ms
        return wire


class CallInfo:
    """Details of the call being handled, for handlers that need them."""

    __slots__ = ("request_id", "contract_name", "contract_version", "operation", "surface", "deadline")

    def __init__(self, request_id, contract_name, contract_version, operation, surface, deadline):
        self.request_id = request_id
        self.contract_name = contract_name
        self.contract_version = contract_version
        self.operation = operation
        self.surface = surface
        self.deadline = deadline  # seconds since the epoch (time.time() scale)

    def remaining(self):
        return self.deadline - time.time()


_current = None


def current_call():
    """The CallInfo of the in-flight call, or None outside a handler."""
    return _current


# --- strict JSON ------------------------------------------------------------


def _pairs(pairs):
    out = {}
    for key, value in pairs:
        if key in out:
            raise ProtocolError("duplicate JSON key")
        out[key] = value
    return out


def _constant(name):
    raise ProtocolError("non-finite JSON number")


def _check_value(root):
    stack = [(root, 0)]
    while stack:
        value, depth = stack.pop()
        if depth > _MAX_DEPTH:
            raise ProtocolError("JSON nesting exceeds 64 levels")
        if isinstance(value, str):
            if _has_surrogate(value):
                raise ProtocolError("unpaired surrogate escape")
        elif isinstance(value, dict):
            for key, child in value.items():
                if _has_surrogate(key):
                    raise ProtocolError("unpaired surrogate escape")
                stack.append((child, depth + 1))
        elif isinstance(value, list):
            for child in value:
                stack.append((child, depth + 1))


def _has_surrogate(text):
    # A valid pair decodes to an astral character, so any code point in the
    # surrogate range left in a decoded string was unpaired.
    for ch in text:
        if "\ud800" <= ch <= "\udfff":
            return True
    return False


def decode_frame(raw):
    """Decode one frame (bytes without the LF) with the Go decoder's rules."""
    if len(raw) > MAX_FRAME_BYTES:
        raise ProtocolError("frame too large")
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        raise ProtocolError("JSON must be UTF-8")
    try:
        value = json.loads(text, object_pairs_hook=_pairs, parse_constant=_constant)
    except ProtocolError:
        raise
    except (ValueError, RecursionError) as exc:
        raise ProtocolError("malformed JSON: %s" % type(exc).__name__)
    _check_value(value)
    return value


def encode_frame(value):
    return json.dumps(value, separators=(",", ":"), ensure_ascii=True, allow_nan=False).encode("ascii")


def _read_frame(stream):
    """Read one LF-terminated frame; None at clean EOF. Bounded by the limit."""
    line = stream.readline(MAX_FRAME_BYTES + 2)
    if not line:
        return None
    if not line.endswith(b"\n"):
        if len(line) > MAX_FRAME_BYTES + 1:
            raise ProtocolError("frame too large")
        raise ProtocolError("unexpected EOF inside a frame")
    return line[:-1]


# --- descriptor -------------------------------------------------------------


def validate_descriptor(d):
    """Raise ValueError unless d is a valid ext.plugin/v1 descriptor."""
    if not isinstance(d, dict) or set(d) - {"apiVersion", "identity", "contracts"}:
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
            if not isinstance(op, dict) or set(op) - {"name", "surface"}:
                raise ValueError("invalid operation")
            name, surface = op.get("name"), op.get("surface", "")
            if (
                not _match(_IDENTIFIER, name)
                or name == "plugin.hello"
                or name in names
                or not isinstance(surface, str)
                or (surface != "" and not _match(_IDENTIFIER, surface))
            ):
                raise ValueError("invalid, reserved, or duplicate operation")
            names.add(name)


def _match(pattern, value):
    return isinstance(value, str) and pattern.match(value) is not None


def load_descriptor(path):
    """Read a descriptor from a package.json (ext.package/v1) or a bare descriptor file."""
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

_ZERO_TIME = -62135596800.0
_REQUEST_FIELDS = {"apiVersion", "id", "plugin", "contract", "operation", "surface", "deadline", "payload"}


def _parse_deadline(value):
    """RFC 3339 to epoch seconds; None when invalid or the zero time."""
    if not isinstance(value, str):
        return None
    m = _TIME.match(value)
    if not m:
        return None
    year, month, day, hour, minute, second = (int(m.group(i)) for i in range(1, 7))
    frac = m.group(7)
    micro = int((frac[1:] + "000000")[:6]) if frac else 0
    tz = m.group(8)
    if tz in ("Z", "z"):
        offset = timedelta(0)
    else:
        sign = 1 if tz[0] == "+" else -1
        offset = sign * timedelta(hours=int(tz[1:3]), minutes=int(tz[4:6]))
    try:
        stamp = datetime(year, month, day, hour, minute, second, micro, tzinfo=timezone(offset)).timestamp()
    except (ValueError, OverflowError, OSError):
        return None
    if stamp == _ZERO_TIME:
        return None  # Go's zero time means "no deadline given"
    return stamp


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
        self._ops = {}
        for c in self._descriptor["contracts"]:
            for op in c["operations"]:
                self._ops[(c["name"], c["version"], op["name"])] = op.get("surface", "")
        ident = self._descriptor["identity"]
        self._identity = {"id": ident["id"], "revision": ident["revision"], "version": ident.get("version", "")}

    def handshake(self):
        return json.loads(json.dumps(self._descriptor))

    def invoke(self, request):
        """Validate one decoded request and return the response object.

        Raises ProtocolError for envelopes the Go host would not even decode
        (unknown fields, wrong types); those end the stream.
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
            or self._ops[key] != surface
        ):
            response["error"] = {"code": "invalid_request", "message": "request does not match selected contract"}
            return response
        limit = min(deadline, time.time() + self._max_call)
        error = None
        result = None
        if time.time() >= limit:
            error = self._failure(TimeoutError("deadline exceeded"))
        else:
            _current = CallInfo(request_id, contract["name"], contract["version"], operation, surface, limit)
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
    """Serve ``guest`` over binary streams until EOF. Raises ProtocolError on violations."""
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
            response = guest.invoke(request)
        frame = encode_frame(response)
        if len(frame) > MAX_FRAME_BYTES:
            response = {
                "apiVersion": API_VERSION,
                "id": response["id"],
                "error": {"code": "response_too_large", "message": "response exceeds the frame limit"},
            }
            frame = encode_frame(response)
        stdout.write(frame + b"\n")
        stdout.flush()


def _answer_hello(guest, request):
    if not isinstance(request, dict) or set(request) - _REQUEST_FIELDS:
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

    descriptor is a dict or the path of a package.json / descriptor file.
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
