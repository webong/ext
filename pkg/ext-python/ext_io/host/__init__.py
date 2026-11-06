"""Host SDK for ext.plugin/v1: runs plugins through the C engine (lib/plugin).

    from ext_io import host

    engine = host.Engine()          # EXT_HOST_LIBRARY, or libext_host on the loader path
    with engine.process(descriptor, "/opt/plugins/echo/1.0.0/echo") as h:
        h.start()
        h.call({"name": "ext.echo", "version": "v1"}, "echo", {"value": 7})

The engine does the work a host must get right: admission, the handshake check
against the descriptor, request and response validation, deadlines, and the
session's lifecycle. This module only moves bytes and policy decisions across
``ctypes``, so it is standard library only, but it needs the engine's shared
library, ``libext_host``. Importing ``ext_io.plugin`` (the guest) never loads it.

Guests may be JSON-line child processes (``Engine.process``) or libraries that
export the C ABI of ``ext_plugin.h`` (``Engine.shared_library``). Native code in a
shared library runs inside this process with its full access.
"""

import ctypes
import ctypes.util
import json
import os
import sys

from ..plugin import RemoteError

__all__ = ["Engine", "Host", "HostError", "RemoteError", "STATUS_NAMES"]

_ABI_VERSION = 2
_BACKEND_EXTENSION_CONTEXT = 3
_BACKEND_PROCESS_LAUNCH = 5

STATUS_NAMES = (
    "ok", "invalid", "denied", "mismatch", "unsupported", "closed", "timeout", "io",
    "nomem", "draining", "not_found", "ambiguous", "canceled", "updating", "capacity",
    "sequence", "protocol",
)
_STATES = ("created", "ready", "draining", "closed", "failed")


class HostError(Exception):
    """A host failure: the engine's status, by name (``denied``, ``closed`` ...)."""

    def __init__(self, status, action=""):
        self.code = status
        self.status = STATUS_NAMES[status] if 0 <= status < len(STATUS_NAMES) else "unknown"
        super().__init__("ext host: %s%s" % (action + ": " if action else "", self.status))


_POLICY = ctypes.CFUNCTYPE(ctypes.c_int32, ctypes.c_void_p, ctypes.POINTER(ctypes.c_uint8), ctypes.c_size_t)
_RELEASE = ctypes.CFUNCTYPE(None, ctypes.c_void_p)


class _Buffer(ctypes.Structure):
    _fields_ = [("data", ctypes.POINTER(ctypes.c_uint8)), ("len", ctypes.c_size_t)]


class _HostOptions(ctypes.Structure):
    _fields_ = [
        ("abi_version", ctypes.c_uint32), ("struct_size", ctypes.c_uint32),
        ("descriptor", ctypes.c_char_p), ("descriptor_len", ctypes.c_size_t),
        ("verify", _POLICY), ("authorize", _POLICY), ("user", ctypes.c_void_p),
    ]


class _BackendOptions(ctypes.Structure):
    _fields_ = [
        ("struct_size", ctypes.c_uint32), ("kind", ctypes.c_uint32),
        ("config", ctypes.c_void_p), ("config_size", ctypes.c_size_t),
    ]


class _Launch(ctypes.Structure):
    _fields_ = [
        ("struct_size", ctypes.c_uint32), ("executable", ctypes.c_char_p),
        ("arguments", ctypes.POINTER(ctypes.c_char_p)), ("argument_count", ctypes.c_size_t),
        ("environment", ctypes.POINTER(ctypes.c_char_p)), ("environment_count", ctypes.c_size_t),
        ("working_directory", ctypes.c_char_p), ("stderr_bytes", ctypes.c_uint32),
    ]


class _Extension(ctypes.Structure):
    _fields_ = [
        ("struct_size", ctypes.c_uint32), ("flags", ctypes.c_uint32), ("user", ctypes.c_void_p),
        ("connect", ctypes.c_void_p), ("invoke", ctypes.c_void_p),
        ("close", ctypes.c_void_p), ("release", ctypes.c_void_p),
    ]


class _SharedLibrary(ctypes.Structure):
    _fields_ = [("struct_size", ctypes.c_uint32), ("path", ctypes.c_char_p)]


class _CallOptions(ctypes.Structure):
    _fields_ = [
        ("struct_size", ctypes.c_uint32), ("timeout_ms", ctypes.c_uint32),
        ("cancel", ctypes.c_void_p), ("value", ctypes.c_void_p),
    ]


def _library_path(path):
    if path:
        return path
    env = os.environ.get("EXT_HOST_LIBRARY")
    if env:
        return env
    found = ctypes.util.find_library("ext_host")
    if found:
        return found
    raise OSError("the ext engine library was not found: set EXT_HOST_LIBRARY to libext_host, or pass its path")


def _milliseconds(seconds):
    ms = int(seconds * 1000)
    if ms < 1:
        raise ValueError("timeout must be at least 1 ms")
    return min(ms, 0xFFFFFFFF)


def _encode(value):
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8")


def _policy(decide):
    """A C policy around ``decide(dict) -> bool``. Any exception denies."""
    if decide is None:
        return _POLICY(lambda user, data, n: 0)

    def call(user, data, n):
        try:
            return 0 if decide(json.loads(ctypes.string_at(data, n).decode("utf-8"))) else 1
        except BaseException:  # never unwind into C
            return 1

    return _POLICY(call)


def _strings(values):
    values = [v.encode("utf-8") if isinstance(v, str) else bytes(v) for v in values]
    array = (ctypes.c_char_p * max(len(values), 1))(*values)
    return array, len(values), values


class Engine:
    """The C engine, loaded from ``path``, ``EXT_HOST_LIBRARY`` or the loader path."""

    def __init__(self, path=None):
        lib = ctypes.CDLL(_library_path(path))
        self._lib = lib
        P, S, I = ctypes.POINTER, ctypes.c_size_t, ctypes.c_int32
        lib.ext_host_abi_version.restype = ctypes.c_uint32
        if lib.ext_host_abi_version() != _ABI_VERSION:
            raise OSError("ext engine ABI %d, this module needs %d" % (lib.ext_host_abi_version(), _ABI_VERSION))
        lib.ext_host_create.argtypes = [P(_HostOptions), P(_BackendOptions), P(ctypes.c_void_p)]
        lib.ext_host_create.restype = I
        lib.ext_host_start_with_options.argtypes = [ctypes.c_void_p, P(_CallOptions)]
        lib.ext_host_start_with_options.restype = I
        lib.ext_host_call.argtypes = [ctypes.c_void_p, ctypes.c_char_p, S, P(_CallOptions), P(_Buffer)]
        lib.ext_host_call.restype = I
        lib.ext_host_invoke_with_options.argtypes = [ctypes.c_void_p, ctypes.c_char_p, S, P(_CallOptions), P(_Buffer)]
        lib.ext_host_invoke_with_options.restype = I
        lib.ext_host_drain_with_options.argtypes = [ctypes.c_void_p, P(_CallOptions)]
        lib.ext_host_drain_with_options.restype = I
        lib.ext_host_close.argtypes = [ctypes.c_void_p]
        lib.ext_host_close.restype = None
        lib.ext_host_destroy.argtypes = [ctypes.c_void_p]
        lib.ext_host_destroy.restype = None
        lib.ext_host_get_state.argtypes = [ctypes.c_void_p]
        lib.ext_host_get_state.restype = ctypes.c_int
        lib.ext_host_process_stderr.argtypes = [ctypes.c_void_p, P(ctypes.c_uint8), S, P(S)]
        lib.ext_host_process_stderr.restype = I
        lib.ext_buffer_free.argtypes = [P(_Buffer)]
        lib.ext_buffer_free.restype = None
        lib.ext_shared_library_backend_create.argtypes = [P(_SharedLibrary), P(_Extension)]
        lib.ext_shared_library_backend_create.restype = I

    def process(self, descriptor, executable, arguments=(), environment=(), working_directory=None,
                stderr_bytes=4096, verify=None, authorize=None):
        """A host for a JSON-line guest started as ``executable`` (an absolute path, no shell).

        ``environment`` is a list of ``NAME=value`` entries added to a scrubbed base of PATH;
        the child's stderr is kept up to ``stderr_bytes`` (``Host.stderr``). ``verify`` sees the
        descriptor before the child starts and ``authorize`` each validated request; each
        returns True to allow. Neither may block or call back into the host."""
        args, argc, keep_args = _strings(arguments)
        env, envc, keep_env = _strings(environment)
        launch = _Launch(ctypes.sizeof(_Launch), os.fsencode(executable), args, argc, env, envc,
                         os.fsencode(working_directory) if working_directory else None, stderr_bytes)
        backend = _BackendOptions(ctypes.sizeof(_BackendOptions), _BACKEND_PROCESS_LAUNCH,
                                  ctypes.cast(ctypes.byref(launch), ctypes.c_void_p), ctypes.sizeof(launch))
        return Host(self, descriptor, backend, verify, authorize, (launch, args, env, keep_args, keep_env))

    def shared_library(self, descriptor, path, verify=None, authorize=None):
        """A host for a guest library that exports the C ABI of ``ext_plugin.h``.

        ``path`` is an absolute path to a verified, revision-specific artifact. The library
        is loaded when the host starts, after ``verify``, and is never unloaded."""
        options = _SharedLibrary(ctypes.sizeof(_SharedLibrary), os.fsencode(path))
        ext = _Extension()
        status = self._lib.ext_shared_library_backend_create(ctypes.byref(options), ctypes.byref(ext))
        if status:
            raise HostError(status, "shared library")
        backend = _BackendOptions(ctypes.sizeof(_BackendOptions), _BACKEND_EXTENSION_CONTEXT,
                                  ctypes.cast(ctypes.byref(ext), ctypes.c_void_p), ctypes.sizeof(ext))
        try:
            return Host(self, descriptor, backend, verify, authorize, (ext,))
        except HostError:
            _RELEASE(ext.release)(ext.user)  # ownership stays with the caller on a failed create
            raise


class Host:
    """One session with one plugin. Use ``Engine.process`` or ``Engine.shared_library``.

    Calls may come from several threads; the engine serializes them for a process or a
    shared library. ``close`` aborts; ``destroy`` (or leaving a ``with`` block) frees the
    host once no call is running."""

    def __init__(self, engine, descriptor, backend, verify, authorize, keep):
        self._lib = engine._lib
        data = descriptor if isinstance(descriptor, (bytes, bytearray)) else _encode(descriptor)
        self._descriptor = bytes(data)
        self._verify = _policy(verify)
        self._authorize = _policy(authorize)
        self._keep = keep
        options = _HostOptions(_ABI_VERSION, ctypes.sizeof(_HostOptions), self._descriptor,
                               len(self._descriptor), self._verify, self._authorize, None)
        handle = ctypes.c_void_p()
        status = self._lib.ext_host_create(ctypes.byref(options), ctypes.byref(backend), ctypes.byref(handle))
        if status:
            raise HostError(status, "create")
        self._handle = handle

    def _options(self, timeout):
        return _CallOptions(ctypes.sizeof(_CallOptions), _milliseconds(timeout), None, None)

    def _live(self):
        if self._handle is None:
            raise HostError(5, "destroyed")
        return self._handle

    def start(self, timeout=3.0):
        """Starts the guest and checks its handshake against the descriptor."""
        options = self._options(timeout)
        status = self._lib.ext_host_start_with_options(self._live(), ctypes.byref(options))
        if status:
            raise HostError(status, "start")

    def _exchange(self, fn, data, timeout, action):
        options = self._options(timeout)
        out = _Buffer()
        status = fn(self._live(), data, len(data), ctypes.byref(options), ctypes.byref(out))
        try:
            if status:
                raise HostError(status, action)
            return ctypes.string_at(out.data, out.len)
        finally:
            if out.data:
                self._lib.ext_buffer_free(ctypes.byref(out))

    def call(self, contract, operation, payload=None, timeout=3.0):
        """Calls an operation and returns its payload (None when it has none).

        A domain error raises ``RemoteError``; a host failure raises ``HostError``."""
        request = {"contract": {"name": contract["name"], "version": contract["version"]}, "operation": operation}
        if payload is not None:
            request["payload"] = payload
        response = json.loads(self._exchange(self._lib.ext_host_call, _encode(request), timeout, operation))
        error = response.get("error")
        if error is not None:
            raise RemoteError(error.get("code", ""), error.get("message", ""),
                              retry_after_ms=error.get("retryAfterMilliseconds", 0))
        return response.get("payload")

    def invoke(self, request, timeout=3.0):
        """Sends a complete request envelope (bytes) and returns the response envelope (bytes)."""
        return self._exchange(self._lib.ext_host_invoke_with_options, bytes(request), timeout, "invoke")

    def drain(self, timeout=3.0):
        """Stops admission and waits for the calls in flight; a timeout reopens admission."""
        options = self._options(timeout)
        status = self._lib.ext_host_drain_with_options(self._live(), ctypes.byref(options))
        if status:
            raise HostError(status, "drain")

    def close(self):
        """Aborts the session; calls in flight end with ``closed``."""
        if self._handle is not None:
            self._lib.ext_host_close(self._handle)

    @property
    def state(self):
        s = self._lib.ext_host_get_state(self._live())
        return _STATES[s] if 0 <= s < len(_STATES) else "unknown"

    @property
    def stderr(self):
        """What a process guest wrote to stderr, bounded and untrusted."""
        size = ctypes.c_size_t()
        buf = (ctypes.c_uint8 * 65536)()
        status = self._lib.ext_host_process_stderr(self._live(), buf, len(buf), ctypes.byref(size))
        if status:
            raise HostError(status, "stderr")
        return bytes(buf[: min(size.value, len(buf))])

    def destroy(self):
        """Frees the host. Call it once, after every call has returned."""
        if self._handle is not None:
            self._lib.ext_host_destroy(self._handle)
            self._handle = None

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        self.destroy()
        return False

    def __del__(self):
        if getattr(self, "_handle", None) is not None and not sys.is_finalizing():
            self.destroy()
