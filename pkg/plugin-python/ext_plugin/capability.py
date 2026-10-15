"""Typed definitions of the SDK's optional capability contracts, with the same
wire shapes and limits as the Go library: ``ext.health``, ``ext.configuration``
and ``ext.stream``. ``Router`` is a handler for ``serve_stdio`` that checks each
payload and result against them before and after your function runs.

    router = ext_plugin.capability.Router()
    router.health(lambda: {"status": "healthy", "ready": True})
    ext_plugin.serve_stdio("package.json", router)
"""

import json

from .guest import RemoteError

MAX_STREAM_ITEMS = 256
MAX_STREAM_BATCH_BYTES = 1 << 20
_MAX_ID = 64
_MAX_NAME = 256
_STATUSES = ("healthy", "degraded", "unhealthy")


def _object(value, fields):
    """value must be a dict with exactly these keys."""
    if not isinstance(value, dict) or set(value) != set(fields):
        raise ValueError("object does not have exactly the fields %s" % (list(fields),))
    return value


def _text(value, limit):
    if not isinstance(value, str) or not value or len(value) > limit:
        raise ValueError("expected a non-empty string of at most %d characters" % limit)


def _integer(value):
    # bool is an int in Python but not a JSON number.
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError("expected an integer")


def health_request(value):
    _object(value, ())
    return value


def health(value):
    _object(value, ("status", "ready"))
    if value["status"] not in _STATUSES or not isinstance(value["ready"], bool):
        raise ValueError("invalid health")
    return value


def configuration(value):
    _object(value, ("instance", "revision", "values"))
    _text(value["instance"], _MAX_NAME)
    _text(value["revision"], _MAX_NAME)
    return value


def configured(value):
    _object(value, ("instance", "revision"))
    if not isinstance(value["instance"], str) or not isinstance(value["revision"], str):
        raise ValueError("invalid configured")
    return value


def stream_open(value):
    return _object(value, ("parameters",))


def stream_opened(value):
    _object(value, ("id",))
    _text(value["id"], _MAX_ID)
    return value


def stream_read(value):
    _object(value, ("id", "sequence", "limit"))
    _text(value["id"], _MAX_ID)
    _integer(value["sequence"])
    _integer(value["limit"])
    if value["sequence"] < 1 or not 1 <= value["limit"] <= MAX_STREAM_ITEMS:
        raise ValueError("invalid read")
    return value


def stream_close(value):
    _object(value, ("id",))
    _text(value["id"], _MAX_ID)
    return value


def stream_batch(value):
    """At most MAX_STREAM_ITEMS items and MAX_STREAM_BATCH_BYTES of JSON."""
    _object(value, ("items", "done"))
    if not isinstance(value["items"], list) or not isinstance(value["done"], bool):
        raise ValueError("invalid batch")
    if len(value["items"]) > MAX_STREAM_ITEMS:
        raise ValueError("too many items")
    size = 0
    for item in value["items"]:
        size += len(json.dumps(item, separators=(",", ":"), ensure_ascii=False).encode("utf-8"))
        if size > MAX_STREAM_BATCH_BYTES:
            raise ValueError("batch too large")
    return value


class Router:
    """Dispatches (contract, operation) to registered functions, validating the
    payload before the call and the result after it. A payload that fails
    validation is an ``invalid_request`` remote error; a result that fails is an
    internal error, because it is the plugin's own bug."""

    def __init__(self):
        self._routes = {}

    def on(self, contract, version, operation, function, check_input=None, check_output=None):
        key = (contract, version, operation)
        if key in self._routes:
            raise ValueError("operation already registered")
        self._routes[key] = (function, check_input, check_output)
        return self

    def health(self, check):
        """check() returns {"status": ..., "ready": ...}."""
        return self.on("ext.health", "v1", "check", lambda _p: check(), health_request, health)

    def configuration(self, update):
        """update(instance, revision, values) returns None; the router answers."""

        def run(payload):
            update(payload["instance"], payload["revision"], payload["values"])
            return {"instance": payload["instance"], "revision": payload["revision"]}

        return self.on("ext.configuration", "v1", "update", run, configuration, configured)

    def stream(self, open_, read, close):
        """open_(parameters) -> id; read(id, sequence, limit) -> {"items", "done"}; close(id)."""
        self.on("ext.stream", "v1", "open", lambda p: {"id": open_(p["parameters"])}, stream_open, stream_opened)
        self.on("ext.stream", "v1", "read", lambda p: read(p["id"], p["sequence"], p["limit"]), stream_read, stream_batch)
        return self.on("ext.stream", "v1", "close", lambda p: (close(p["id"]) or {}), stream_close, lambda v: _object(v, ()))

    def __call__(self, contract, operation, payload):
        name, version = contract if isinstance(contract, tuple) else (contract, "v1")
        entry = self._routes.get((name, version, operation))
        if entry is None:
            raise RemoteError("unsupported", "operation is not implemented")
        function, check_input, check_output = entry
        try:
            if check_input:
                check_input(payload)
        except ValueError:
            raise RemoteError("invalid_request", "payload does not match the contract")
        result = function(payload)
        if check_output:
            check_output(result)  # a ValueError here is the plugin's bug and surfaces as an internal error
        return result
