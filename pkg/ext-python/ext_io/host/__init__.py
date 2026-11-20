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
import weakref

from ..plugin import RemoteError

__all__ = ["Engine", "Host", "HostError", "Registry", "RemoteError", "ReverseCall", "STATUS_NAMES"]

_ABI_VERSION = 2
_BACKEND_EXTENSION_CONTEXT = 3
_BACKEND_PROCESS_LAUNCH = 5

STATUS_NAMES = (
    "ok", "invalid", "denied", "mismatch", "unsupported", "closed", "timeout", "io",
    "nomem", "draining", "not_found", "ambiguous", "canceled", "updating", "capacity",
    "sequence", "protocol", "expired", "exists", "missing",
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
_REVERSE_PAYLOAD, _REVERSE_PUBLIC_ERROR = 0, 1


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


_CONTEXT_POLICY = ctypes.CFUNCTYPE(ctypes.c_int32, ctypes.c_void_p, ctypes.POINTER(_CallOptions),
                                   ctypes.POINTER(ctypes.c_uint8), ctypes.c_size_t)
_REVERSE_EMIT = ctypes.CFUNCTYPE(ctypes.c_int32, ctypes.c_void_p, ctypes.c_uint32,
                                 ctypes.POINTER(ctypes.c_uint8), ctypes.c_size_t)
_REVERSE_HANDLER = ctypes.CFUNCTYPE(ctypes.c_int32, ctypes.c_void_p, ctypes.POINTER(_CallOptions),
                                    ctypes.POINTER(ctypes.c_uint8), ctypes.c_size_t, _REVERSE_EMIT, ctypes.c_void_p)


class _Services(ctypes.Structure):
    _fields_ = [
        ("struct_size", ctypes.c_uint32), ("user", ctypes.c_void_p),
        ("authorize", _CONTEXT_POLICY), ("handle", _REVERSE_HANDLER),
        ("timeout_ms", ctypes.c_uint32), ("max_payload_bytes", ctypes.c_uint32),
        ("max_in_flight", ctypes.c_uint32), ("session_value", ctypes.c_void_p),
    ]


class _RegistryEdge(ctypes.Structure):
    _fields_ = [("struct_size", ctypes.c_uint32)] + [
        (name, ctypes.c_char_p) for name in
        ("from_name", "from_version", "from_operation", "to_name", "to_version", "to_operation")
    ]


class ReverseCall:
    """One reverse request a plugin made, for the hook that serves it: ``request`` is the
    Request as a dict (apiVersion, id, plugin, contract, operation, surface, deadline,
    payload and, for a call-scoped request, parent), ``remaining`` the seconds left to it."""

    def __init__(self, lib, options, data, n):
        self.request = json.loads(ctypes.string_at(data, n).decode("utf-8"))
        self.remaining = options.contents.timeout_ms / 1000.0
        self._lib, self._cancel = lib, options.contents.cancel

    def canceled(self):
        """True once the call it belongs to ended, the caller canceled it or the host closed."""
        return bool(self._cancel) and bool(self._lib.ext_cancel_is_signaled(self._cancel))


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
        lib.ext_host_set_services.argtypes = [ctypes.c_void_p, P(_Services)]
        lib.ext_host_set_services.restype = I
        lib.ext_cancel_is_signaled.argtypes = [ctypes.c_void_p]
        lib.ext_cancel_is_signaled.restype = ctypes.c_int
        lib.ext_registry_create.argtypes = [P(ctypes.c_void_p)]
        lib.ext_registry_create.restype = I
        lib.ext_registry_register.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_char_p, ctypes.c_void_p]
        lib.ext_registry_register.restype = I
        lib.ext_registry_unregister.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_char_p]
        lib.ext_registry_unregister.restype = I
        lib.ext_registry_route.argtypes = [ctypes.c_void_p, P(_RegistryEdge)]
        lib.ext_registry_route.restype = I
        lib.ext_registry_attach.argtypes = [ctypes.c_void_p, ctypes.c_void_p, P(_Services)]
        lib.ext_registry_attach.restype = I
        lib.ext_registry_destroy.argtypes = [ctypes.c_void_p]
        lib.ext_registry_destroy.restype = None

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
        self._services = None       # the reverse hooks, kept alive with the host
        self._source_of = None      # the Registry this host is a source of
        self._registered = []       # (Registry, name, version) this host serves

    def _install(self, authorize, handle, timeout, max_payload_bytes, max_in_flight, registry):
        lib = self._lib

        def policy(user, options, data, n):
            try:
                return 0 if authorize(ReverseCall(lib, options, data, n)) else 1
            except BaseException:  # never unwind into C; a policy that fails denies
                return 1

        def serve(user, options, data, n, emit, sink):
            try:
                try:
                    reply = _encode(handle(ReverseCall(lib, options, data, n)))
                    kind = _REVERSE_PAYLOAD
                except RemoteError as e:
                    error = {"code": e.code, "message": e.message}
                    if e.retry_after_ms:
                        error["retryAfterMilliseconds"] = e.retry_after_ms
                    reply, kind = _encode(error), _REVERSE_PUBLIC_ERROR
                buf = (ctypes.c_uint8 * len(reply)).from_buffer_copy(reply)
                return emit(sink, kind, buf, len(reply))
            except BaseException:  # the plugin sees operation_failed
                return 7

        hooks = (_CONTEXT_POLICY(policy), _REVERSE_HANDLER(serve) if handle else _REVERSE_HANDLER())
        services = _Services(ctypes.sizeof(_Services), None, hooks[0], hooks[1],
                             0 if timeout is None else _milliseconds(timeout),
                             max_payload_bytes, max_in_flight, None)
        if registry is None:
            status = lib.ext_host_set_services(self._live(), ctypes.byref(services))
        else:
            status = lib.ext_registry_attach(registry._handle, self._live(), ctypes.byref(services))
        if status:
            raise HostError(status, "services")
        self._services = hooks
        if registry is not None:
            self._source_of = registry

    def serve(self, authorize, handle, timeout=None, max_payload_bytes=0, max_in_flight=0):
        """Offers the plugin the operations its descriptor lists under ``reverse``. Call it
        before ``start``. The engine admits or refuses each request with a stable code;
        ``authorize(call)`` (True allows) and ``handle(call)`` are the host's policy, called
        with a ``ReverseCall``. ``handle`` returns a JSON value (None is null), or raises
        ``RemoteError`` for an error the plugin receives as it is; any other exception reaches
        it as ``operation_failed``. Both run on engine threads, up to ``max_in_flight`` at a
        time, must honor ``call.canceled()`` and must not call back into this host. Zero
        bounds use the defaults (30 s, 1 MiB, 4)."""
        if not callable(authorize) or not callable(handle):
            raise TypeError("authorize and handle are required")
        self._install(authorize, handle, timeout, max_payload_bytes, max_in_flight, None)

    def attach(self, registry, authorize, timeout=None, max_payload_bytes=0, max_in_flight=0):
        """Makes this host a source of ``registry``, in place of ``serve``: its plugin's reverse
        calls are served by forward calls to the hosts registered there, along the declared
        edges, after ``authorize(call)`` allowed them. Call it before ``start``."""
        if not isinstance(registry, Registry):
            raise TypeError("registry must be a Registry")
        if not callable(authorize):
            raise TypeError("authorize is required")
        self._install(authorize, None, timeout, max_payload_bytes, max_in_flight, registry)

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
        """Frees the host. Call it once, after every call has returned. A host that serves a
        contract in a registry leaves it first."""
        if self._handle is not None:
            if self._registered:
                # Calls routed here end once the host is closed; unregister waits for them.
                self._lib.ext_host_close(self._handle)
                for registry, name, version in self._registered:
                    self._lib.ext_registry_unregister(registry._handle, name, version)
                    registry._targets.pop((name, version), None)
                self._registered = []
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


class Registry:
    """A guest registry (``ext_registry_*`` in ext_host.h): a reverse call one guest makes is
    served by a forward call to another guest, along an edge declared with ``route``. An
    undeclared edge is refused with ``reverse_undeclared``, an unregistered target with
    ``reverse_unavailable``, and a call that would re-enter a host already on its call chain
    with ``denied``; the target's answer passes through unchanged. A host is a source once
    ``Host.attach`` names the registry, and a target once ``register`` names a contract it
    serves; it leaves the registry when it is destroyed. Hosts keep the registry alive. Safe
    to use from several threads; failures raise ``HostError``."""

    def __init__(self, engine):
        self._lib = engine._lib
        handle = ctypes.c_void_p()
        status = self._lib.ext_registry_create(ctypes.byref(handle))
        if status:
            raise HostError(status, "registry")
        self._handle = handle
        self._targets = {}  # (name, version) -> the Host that serves it, weakly

    @staticmethod
    def _names(*values):
        return [v.encode("utf-8") for v in values]

    def register(self, contract, host):
        """Names ``host`` (started, and listing the contract) as the one serving ``contract``."""
        name, version = self._names(contract["name"], contract["version"])
        status = self._lib.ext_registry_register(self._handle, name, version, host._live())
        if status:
            raise HostError(status, "register")
        host._registered.append((self, name, version))
        self._targets[(name, version)] = weakref.ref(host)

    def unregister(self, contract):
        """Removes the contract and waits for the forward calls routed to its host."""
        name, version = self._names(contract["name"], contract["version"])
        status = self._lib.ext_registry_unregister(self._handle, name, version)
        if status:
            raise HostError(status, "unregister")
        ref = self._targets.pop((name, version), None)
        host = ref() if ref is not None else None
        if host is not None:
            host._registered = [r for r in host._registered if r != (self, name, version)]

    def route(self, source, target):
        """A reverse call of ``source`` ({name, version, operation}) is served by a call of
        ``target``."""
        names = self._names(source["name"], source["version"], source["operation"],
                            target["name"], target["version"], target["operation"])
        edge = _RegistryEdge(ctypes.sizeof(_RegistryEdge), *names)
        status = self._lib.ext_registry_route(self._handle, ctypes.byref(edge))
        if status:
            raise HostError(status, "route")

    def __del__(self):
        # Every host attached to it or registered with it holds a reference, so none is alive.
        if getattr(self, "_handle", None) is not None and not sys.is_finalizing():
            self._lib.ext_registry_destroy(self._handle)
            self._handle = None
