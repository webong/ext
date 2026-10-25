"""Guest SDK for ext.plugin/v1 over bounded JSON lines (stdlib only).

    import ext_plugin

    def handler(contract, operation, payload):
        if operation == "busy":
            raise ext_plugin.RemoteError("busy", "try later", retry_after_ms=250)
        return payload

    ext_plugin.serve_stdio("plugin.json", handler)

The module answers ``plugin.hello`` with the descriptor, validates every request
the way the Go ``plugin.ValidateRequest`` does, and calls the handler
sequentially. It never writes to stdout except protocol frames.
"""

from . import capability, frames
from .frames import (
    DROPPED,
    SENT,
    ContractRef,
    FrameError,
    Progress,
    ProgressLimits,
    ReverseCaller,
    split_progress_text,
)
from .guest import (
    API_VERSION,
    MAX_FRAME_BYTES,
    MAX_RETRY_AFTER_MS,
    CallInfo,
    Guest,
    ProtocolError,
    RemoteError,
    current_call,
    load_descriptor,
    serve,
    serve_stdio,
    validate_descriptor,
)

__all__ = [
    "capability",
    "frames",
    "DROPPED",
    "SENT",
    "ContractRef",
    "FrameError",
    "Progress",
    "ProgressLimits",
    "ReverseCaller",
    "split_progress_text",
    "API_VERSION",
    "MAX_FRAME_BYTES",
    "MAX_RETRY_AFTER_MS",
    "CallInfo",
    "Guest",
    "ProtocolError",
    "RemoteError",
    "current_call",
    "load_descriptor",
    "serve",
    "serve_stdio",
    "validate_descriptor",
]
