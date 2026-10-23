"""What the guest and frames modules share: the wire limits, the patterns, strict
JSON decoding and encoding, and the RFC 3339 deadline."""
import json
import re
from datetime import datetime, timedelta, timezone

API_VERSION = "ext.plugin/v1"
MAX_FRAME_BYTES = 24 << 20
MAX_RETRY_AFTER_MS = 365 * 24 * 60 * 60 * 1000
MAX_MESSAGE_BYTES = 4096
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


def _match(pattern, value):
    return isinstance(value, str) and pattern.match(value) is not None


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


# --- time -------------------------------------------------------------------

_ZERO_TIME = -62135596800.0


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
