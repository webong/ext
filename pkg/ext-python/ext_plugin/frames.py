"""Progress events and reverse calls for a guest (docs/plugin-guest-frames.md).

A guest on a transport that carries frames can report progress while a call runs and
call its host. The rules are the Go contract's, pinned by
lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases}.json. This guest runs
*inline*: it has no reader of its own, so the thread that waits for the host's answer
to a reverse call reads the connection itself, which suffices because the host sends
nothing but answers during a call. A call therefore has one reverse call in flight, from
the handler's thread, and no session-scoped caller exists.
"""
import threading
import time
from collections import namedtuple
from datetime import datetime, timezone

from ._core import (
    API_VERSION,
    MAX_FRAME_BYTES,
    MAX_MESSAGE_BYTES,
    MAX_RETRY_AFTER_MS,
    ProtocolError,
    RemoteError,
    _IDENTIFIER,
    _REVISION,
    _match,
    _parse_deadline,
    _read_frame,
    decode_frame,
    encode_frame,
)

MAX_PROGRESS_TEXT = 2048
DEFAULT_PROGRESS_EVENTS = 50
DEFAULT_PROGRESS_BYTES = 64 * 1024

#: What progress() returns: the event was written, or the rate limit dropped it (routine).
SENT = "sent"
DROPPED = "dropped"

ProgressLimits = namedtuple("ProgressLimits", "events_per_second bytes_per_second")
ContractRef = namedtuple("ContractRef", "name version")


class FrameError(Exception):
    """A progress or reverse-call operation failed locally (a host's refusal is a RemoteError).

    ``code`` is ``unsupported`` (no reporter or caller on this call), ``invalid`` (the event or
    request breaks the rules; nothing was sent), ``closed`` (the call is over or the connection
    ended), or ``protocol`` (the host broke the framing rules; the session ends).
    """

    def __init__(self, code, message=None):
        super().__init__(message or code)
        self.code = code


class Progress:
    """One progress event: a ``progress`` count of ``completed`` out of ``total`` (zero when not
    known), or a ``log`` line, optionally on a ``stream`` (``out`` or ``err``). Text is at most
    2048 bytes of UTF-8; use :func:`split_progress_text` to cut a longer line."""

    __slots__ = ("kind", "stream", "text", "completed", "total")

    def __init__(self, kind, stream="", text="", completed=0, total=0):
        self.kind = kind
        self.stream = stream
        self.text = text
        self.completed = completed
        self.total = total

    @classmethod
    def log(cls, text):
        return cls("log", text=text)

    @classmethod
    def out(cls, text):
        """A log line of a program's standard output."""
        return cls("log", "out", text)

    @classmethod
    def err(cls, text):
        """A log line of a program's standard error."""
        return cls("log", "err", text)

    @classmethod
    def fraction(cls, completed, total):
        """``completed`` of ``total``; zero when the total is not known."""
        return cls("progress", completed=completed, total=total)


def _violation():
    raise ProtocolError("plugin protocol violation")


def utf8_length(text):
    """The length of ``text`` in bytes of UTF-8, or -1 when it is not text (an unpaired surrogate)."""
    try:
        return len(text.encode("utf-8"))
    except UnicodeEncodeError:
        return -1


def split_progress_text(text):
    """Cuts text into pieces that each fit one event: at most 2048 bytes of UTF-8, never ending
    inside a character. A character that is not text (an unpaired surrogate) becomes U+FFFD."""
    pieces = []
    piece = []
    size = 0
    for ch in text:
        if "\ud800" <= ch <= "\udfff":
            ch = "�"
        n = len(ch.encode("utf-8"))
        if size + n > MAX_PROGRESS_TEXT:
            pieces.append("".join(piece))
            piece = []
            size = 0
        piece.append(ch)
        size += n
    if piece:
        pieces.append("".join(piece))
    return pieces


# --- the sender's rate limit --------------------------------------------------


class Limiter:
    """A token bucket counted in events and bytes of text, with a burst of twice the rate, and a
    count of events dropped since the last one that passed. Time is a monotonic clock in
    nanoseconds, given by the caller."""

    def __init__(self, events_per_second=0, bytes_per_second=0, now_ns=0):
        self._event_rate = float(events_per_second if events_per_second > 0 else DEFAULT_PROGRESS_EVENTS)
        self._byte_rate = float(bytes_per_second if bytes_per_second > 0 else DEFAULT_PROGRESS_BYTES)
        self._events = 2 * self._event_rate
        self._bytes = 2 * self._byte_rate
        self._last = now_ns
        self._dropped = 0

    def admit(self, text_bytes, now_ns):
        """The number of events dropped since the previous one that passed (which the caller
        attaches to this event), or None when the rate limit drops it."""
        delta = now_ns - self._last
        # Seconds as Go's Duration.Seconds computes them.
        whole = delta // 1_000_000_000 if delta >= 0 else -(-delta // 1_000_000_000)
        elapsed = float(whole) + float(delta - whole * 1_000_000_000) / 1e9
        if elapsed > 0:
            self._events = min(self._events + elapsed * self._event_rate, 2 * self._event_rate)
            self._bytes = min(self._bytes + elapsed * self._byte_rate, 2 * self._byte_rate)
            self._last = now_ns
        if self._events < 1 or self._bytes < text_bytes:
            self._dropped += 1
            return None
        self._events -= 1
        self._bytes -= text_bytes
        reported = self._dropped
        self._dropped = 0
        return reported

    def pending(self):
        """The drops not yet reported, reset to zero."""
        reported = self._dropped
        self._dropped = 0
        return reported


# --- judging frames -----------------------------------------------------------


def _strict(value, fields):
    if not isinstance(value, dict) or set(value) - set(fields):
        _violation()
    return value


def _text(m, key):
    # A member Go decodes into a string: absent and null are empty.
    v = m.get(key)
    if v is None:
        return ""
    if not isinstance(v, str):
        _violation()
    return v


def _count(m, key):
    # A member Go decodes into a uint64: absent and null are zero; anything but a whole number
    # that fits is refused (1.0 and 1e2 are not whole numbers to Go).
    v = m.get(key)
    if v is None:
        return 0
    if type(v) is not int or not 0 <= v < 1 << 64:
        _violation()
    return v


def _check_event(kind, stream, text, completed, total):
    size = utf8_length(text)
    if (
        kind not in ("progress", "log")
        or stream not in ("", "out", "err")
        or (stream != "" and kind != "log")
        or size < 0
        or size > MAX_PROGRESS_TEXT
        or (total != 0 and completed > total)
    ):
        _violation()


_PROGRESS_FIELDS = ("frame", "apiVersion", "parent", "kind", "stream", "text", "completed", "total", "dropped")


def validate_progress_frame(v):
    """Judges a progress frame (a decoded value): returns its parent (the call it belongs to) and
    the event as ``(kind, stream, text, completed, total, dropped)``; raises ProtocolError."""
    _strict(v, _PROGRESS_FIELDS)
    parent = _text(v, "parent")
    event = (_text(v, "kind"), _text(v, "stream"), _text(v, "text"), _count(v, "completed"), _count(v, "total"), _count(v, "dropped"))
    if _text(v, "frame") != "progress" or _text(v, "apiVersion") != API_VERSION or not _match(_REVISION, parent):
        _violation()
    _check_event(*event[:5])
    return parent, event


_REVERSE_REQUEST_FIELDS = ("frame", "apiVersion", "id", "parent", "contract", "operation", "deadline", "payload")


def validate_reverse_request(v):
    """Judges a reverse request frame, as the host reads it; raises ProtocolError."""
    _strict(v, _REVERSE_REQUEST_FIELDS)
    ref = _strict(v.get("contract"), ("name", "version"))
    parent = _text(v, "parent")
    if (
        _text(v, "frame") != "reverse_request"
        or _text(v, "apiVersion") != API_VERSION
        or not _match(_REVISION, _text(v, "id"))
        or (parent != "" and not _match(_REVISION, parent))
        or not _match(_IDENTIFIER, _text(ref, "name"))
        or not _match(_REVISION, _text(ref, "version"))
        or not _match(_IDENTIFIER, _text(v, "operation"))
        or _parse_deadline(v.get("deadline")) is None
    ):
        _violation()


_REVERSE_RESPONSE_FIELDS = ("frame", "apiVersion", "id", "payload", "error")


def validate_reverse_response(v, request_id):
    """Judges a reverse response, which a guest accepts only for the request it is waiting on.
    Returns ``(payload, None)`` or ``(None, RemoteError)``; raises ProtocolError."""
    _strict(v, _REVERSE_RESPONSE_FIELDS)
    if _text(v, "frame") != "reverse_response" or _text(v, "apiVersion") != API_VERSION or _text(v, "id") != request_id:
        _violation()
    failed = v.get("error") is not None
    # A response carries a payload (null counts) or an error, never both or neither.
    if failed == ("payload" in v):
        _violation()
    if not failed:
        return v["payload"], None
    e = _strict(v["error"], ("code", "message", "retryAfterMilliseconds"))
    retry = _count(e, "retryAfterMilliseconds")
    message = _text(e, "message")
    size = utf8_length(message)
    if not _match(_IDENTIFIER, _text(e, "code")) or size < 0 or size > MAX_MESSAGE_BYTES or retry > MAX_RETRY_AFTER_MS:
        _violation()
    return None, RemoteError(e["code"], message, retry)


# --- the connection -------------------------------------------------------------


def _format_time(seconds):
    # Milliseconds, rounded down, so the instant is never after the call's own.
    moment = datetime.fromtimestamp(seconds, timezone.utc)
    return moment.strftime("%Y-%m-%dT%H:%M:%S.") + "%03dZ" % (moment.microsecond // 1000)


class Link:
    """One connection with the host: frames written one at a time, and the read side that the
    thread waiting for an answer drives. ``failure`` is set when the host broke the rules."""

    def __init__(self, stdin, stdout):
        self._in = stdin
        self._out = stdout
        self._write = threading.Lock()
        self.owner = threading.get_ident()
        self._issued = 0
        self.ended = False
        self.failure = None

    def write(self, frame):
        """Writes one encoded frame and its line feed; frames never interleave."""
        with self._write:
            self._out.write(frame + b"\n")
            self._out.flush()

    def reverse(self, parent, name, version, operation, payload, deadline):
        """Calls a host operation and waits for the answer. ``parent`` is the id of the call it
        belongs to; ``deadline`` is epoch seconds. Returns the host's payload."""
        if self.ended:
            raise FrameError("closed", "plugin connection ended")
        self._issued += 1
        request_id = "g%d" % self._issued
        frame = {"frame": "reverse_request", "apiVersion": API_VERSION, "id": request_id}
        if parent:
            frame["parent"] = parent
        frame.update(
            {
                "contract": {"name": name, "version": version},
                "operation": operation,
                "deadline": _format_time(deadline),
                "payload": payload,
            }
        )
        try:
            validate_reverse_request(frame)
            data = encode_frame(frame)
            if len(data) > MAX_FRAME_BYTES:
                raise ValueError("frame too large")
            decode_frame(data)  # the payload must read back as the host reads it
        except (ProtocolError, TypeError, ValueError, OverflowError, RecursionError):
            raise FrameError("invalid", "reverse request breaks the rules")
        try:
            self.write(data)
        except (OSError, ValueError):
            self.ended = True
            raise FrameError("closed", "plugin connection ended")
        # The host sends nothing but this answer during a call.
        try:
            raw = _read_frame(self._in)
        except ProtocolError:
            raise self._violated()
        except (OSError, ValueError):
            raw = None
        if raw is None:
            self.ended = True
            raise FrameError("closed", "plugin connection ended")
        try:
            answer = decode_frame(raw)
            if not isinstance(answer, dict) or answer.get("frame") != "reverse_response" or answer.get("id") != request_id:
                _violation()
            result, error = validate_reverse_response(answer, request_id)
        except ProtocolError:
            raise self._violated()
        if error is not None:
            raise error
        return result

    def _violated(self):
        self.failure = ProtocolError("plugin protocol violation")
        self.ended = True
        return FrameError("protocol", "plugin protocol violation")


class ReverseCaller:
    """Calls an operation the host offers (declared in the descriptor's ``reverse`` list) and
    waits for the answer. A guest that reads inline makes one call at a time, from the thread
    that runs the handler."""

    #: Several reverse calls cannot be outstanding at once.
    concurrent = False

    def __init__(self, link, declared, parent, deadline):
        self._link = link
        self._declared = declared
        self._parent = parent
        self._deadline = deadline
        self.over = False

    def call(self, contract, operation, payload=None, timeout=None):
        """Calls ``operation`` of ``contract`` (a ``(name, version)`` pair) with a JSON payload and
        returns the host's payload. The host's refusal or error raises RemoteError with its code
        (``reverse_undeclared`` when the descriptor does not list it, ``denied``, ``busy``,
        ``too_large``, ``expired``, ...); a local failure raises FrameError. ``timeout`` seconds
        bound the deadline the host is given, never past the call's own; the wait itself ends with
        the host's answer or the end of the connection, since standard input cannot be read with a
        timeout."""
        name, version = contract
        if (name, version, operation) not in self._declared:
            raise RemoteError("reverse_undeclared", "reverse call refused")
        if self.over:
            raise FrameError("closed", "the call is over")
        if threading.get_ident() != self._link.owner:
            raise FrameError("unsupported", "reverse calls are made from the handler's thread")
        deadline = self._deadline
        if timeout is not None:
            deadline = min(deadline, time.time() + max(0.0, timeout))
        return self._link.reverse(self._parent, name, version, operation, payload, deadline)


class Reporter:
    """Sends the progress events of one call, within the rate limit the host set."""

    def __init__(self, link, parent, limits):
        self._link = link
        self._parent = parent
        self._limiter = Limiter(limits.events_per_second, limits.bytes_per_second, time.monotonic_ns())
        self._lock = threading.Lock()
        self._finished = False

    def report(self, event):
        """Events reach the host in the order they are reported, and none after the call is over."""
        try:
            fields = (event.kind, event.stream, event.text, event.completed, event.total)
            if (
                not isinstance(event.kind, str)
                or not isinstance(event.stream, str)
                or not isinstance(event.text, str)
                or any(type(n) is not int or n < 0 for n in fields[3:])
            ):
                _violation()
            _check_event(*fields)
        except (ProtocolError, AttributeError):
            raise FrameError("invalid", "progress event breaks the rules")
        with self._lock:
            dropped = self._limiter.admit(utf8_length(event.text), time.monotonic_ns())
            if dropped is None:
                return DROPPED
            if self._finished:
                raise FrameError("closed", "the call is over")
            self._write(event.kind, event.stream, event.text, event.completed, event.total, dropped)
        return SENT

    def finish(self):
        """Ends reporting after a marker for events never reported: nothing may follow the response."""
        with self._lock:
            if self._finished:
                return
            self._finished = True
            pending = self._limiter.pending()
            if pending > 0:
                try:
                    self._write("log", "", "", 0, 0, pending)
                except FrameError:
                    pass  # the connection is gone; there is nobody to tell

    def _write(self, kind, stream, text, completed, total, dropped):
        frame = {"frame": "progress", "apiVersion": API_VERSION, "parent": self._parent, "kind": kind}
        for key, value in (("stream", stream), ("text", text), ("completed", completed), ("total", total), ("dropped", dropped)):
            if value:
                frame[key] = value
        try:
            self._link.write(encode_frame(frame))
        except (OSError, ValueError):
            raise FrameError("closed", "plugin connection ended")
