# ext for Python (`ext-io`): plugin guest and host SDK

> **Why `ext-io`:** the bare name `ext` is taken on crates.io, PyPI and npm, so `ext-io` is the one package for all of ext's APIs, one namespace per area. Only `plugin` is built so far; `system`, `web` and `int` are planned.

Standard-library-only guest for `ext.plugin/v1` over JSON lines (Python 3.8+).
Any ext host runs it, including the Go `pkg/ext-go/engine/host/process` backend and the Python host
below, or any JSON-line client.

```python
from ext_io import plugin

def handler(contract, operation, payload):
    if operation == "busy":
        raise plugin.RemoteError("busy", "try later", retry_after_ms=250)
    return payload                      # any JSON-serializable value

plugin.serve_stdio("plugin.json", handler)   # or a descriptor dict
```

The manifest file is `plugin.json` (it was `package.json` before ext v0.4.0, which freed that name for
npm and Node tooling). `serve_stdio` accepts the manifest or a bare descriptor file by structure, so only the
file name in your code changes.

Import `ext_io.plugin` from this directory (add it to `PYTHONPATH`, or `pip install` it).

- `serve_stdio(descriptor, handler, max_call_seconds=30, redact_errors=False)`:
  serves until EOF. `descriptor` is a dict or the path of an `ext.package/v1`
  `plugin.json` (its `descriptor` is used) or a bare descriptor file.
- `Guest(descriptor, handler, ...)`, `serve(guest, stdin, stdout)`: the same
  with caller-supplied binary streams, for tests.
- `RemoteError(code, message, retry_after_ms=None)`: sent as the structured
  error. Any other exception becomes `provider_error` with `str(exc)` cut to
  4096 bytes; that text reaches the host, so pass `redact_errors=True` to send
  a generic `operation_failed` instead.
- `current_call()`: request id, contract, operation, surface and absolute
  `deadline` for the in-flight call. Calls are sequential and cannot be
  interrupted, so long handlers should check `remaining()`.
- `load_descriptor(path)`, `validate_descriptor(d)`.

## Report progress and call the host

A guest served by `serve` can report progress while a call runs and call back into its host.
An operation that reports declares `"progress": true` in the descriptor; the host operations a
guest may call are the descriptor's `reverse` list. Both go through `current_call()`:

```python
def handler(contract, operation, payload):
    call = plugin.current_call()
    # Events reach the host in order, before the result. DROPPED is routine: the rate limit the
    # host set dropped it and counts it for the next event.
    call.progress(plugin.Progress.log("starting"))
    call.progress(plugin.Progress.fraction(1, 3))
    # The host's refusal or error raises RemoteError with its code (denied, busy, too_large,
    # expired, ...).
    return call.reverse.call(("host.svc", "v1"), "sign", payload)
```

`call.progress` raises `FrameError` whose `code` is `unsupported` (the operation did not
declare progress, the host asked for no events, or there is no frame channel: stop reporting),
`invalid` (the event breaks the rules; nothing is sent) or `closed` (the call is over).
`call.progress_limits` is the rate the host set, and `split_progress_text` cuts a long line into
events that each fit. This guest reads *inline*: it has no reader of its own, so the thread that
waits for the host's answer reads the connection itself. The host sends nothing but that answer
during a call, so this suffices, with limits: one reverse call at a time, from the handler's
thread (`call.reverse.concurrent` is false and another thread gets `unsupported`);
`call.session_reverse` is always `None`; and the wait ends with the answer or the end of the
connection, since standard input cannot be read with a timeout (`timeout=` bounds the deadline
the host is given). A frame the host may not send ends the session with `ProtocolError` and the
call in flight is not answered; when the input ends during a call, the call is answered and the
session ends. `Guest.invoke(request)` on its own has no frames: `call.reverse` is `None`.

`tests/test_fixtures.py` reproduces the shared fixtures
(`lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases,model-cases}.json`, computed by the
Go contract) with no exceptions, `tests/test_serve_frames.py` covers the router, and
`tests/frames_guest.py` is the frames conformance guest of
[docs/plugin-guest-frames.md](https://github.com/webong/ext/blob/main/docs/plugin-guest-frames.md), which
`scripts/plugin-crosslang.sh` runs through the Go session and the C engine (it answers the
operations that need a reader of its own with the public error `unsupported`).

Behavior matches the Go `jsonline` server: `plugin.hello` is required first and
answered with the descriptor; frames are at most 24 MiB and read with a bound;
envelopes with unknown fields, duplicate keys, trailing values, non-finite
numbers, unpaired surrogates or nesting beyond 64 levels end the stream; each
request is checked for API version, id, identity, contract, operation, surface
and deadline, and a mismatch returns `invalid_request`. Stdout carries only
protocol frames: file descriptor 1 and `sys.stdout` are redirected to stderr.

Tests: `python3 -I -m unittest discover -s tests` here (CI runs it), and the Go suite in
`pkg/ext-go/engine/plugintest` (`TestPythonGuest`) runs this guest through
`process.Start` against the shared conformance fixtures.

## Host plugins from Python

`ext_io.host` runs plugins through the C engine (`lib/plugin`), so a Python program can host any
ext plugin, whatever language it is written in. The engine does what a host must get right:
admission, the handshake check against the descriptor, request and response validation,
deadlines and the session's lifecycle. The module only moves bytes and policy decisions across
`ctypes`, so it is still standard library only, but it needs the engine's shared library,
`libext_host`. Take it from an `ext-engine-vX.Y.Z` release (`lib/libext_host.so`, `lib/libext_host.dylib`
or `bin/ext_host.dll` in the archive for your platform), or build it with
`cmake -S lib/plugin -B build -DEXT_BUILD_SHARED=ON`, and point `EXT_HOST_LIBRARY` at it, or pass its path
to `Engine`. Importing the guest (`ext_io.plugin`)
never loads the host.

```python
from ext_io import host, plugin

engine = host.Engine()                                   # EXT_HOST_LIBRARY, or the loader path
descriptor = plugin.load_descriptor("plugin.json")
contract = {"name": "ext.echo", "version": "v1"}

# A JSON-line guest, started as a child process (an absolute path, no shell).
with engine.process(descriptor, "/opt/plugins/echo/1.0.0/echo",
                    verify=lambda d: d["identity"]["id"] == "example/echo",
                    authorize=lambda request: request["operation"] == "echo") as h:
    h.start(timeout=3.0)
    print(h.call(contract, "echo", {"value": 7}))         # {'value': 7}

# A guest built as a C ABI library (ext_plugin.h): C, C++, Rust, Zig, Go c-shared or Swift.
with engine.shared_library(descriptor, "/opt/plugins/echo/1.0.0/libecho.so") as h:
    h.start()
    try:
        h.call(contract, "busy")
    except plugin.RemoteError as e:                       # a domain error from the plugin
        print(e.code, e.message)
    except host.HostError as e:                           # a host failure: e.status is "denied", "closed" ...
        print(e.status)
```

- `verify(descriptor)` runs before the guest starts and `authorize(request)` before each call;
  each returns True to allow, gets the JSON as a dict, and an exception denies. They run on the
  calling thread and must not call back into the host.
- `call` returns the payload, or raises `plugin.RemoteError` for a domain error and
  `host.HostError` for a host failure. `invoke` sends and returns complete envelopes as bytes.
- Calls may come from several threads; the engine serializes them for a process or a shared
  library, and `ctypes` releases the GIL while a call runs. `close` aborts, `drain` waits for the
  calls in flight, and leaving the `with` block (or `destroy`) frees the host.
- A shared library is loaded when the host starts, after `verify`, and is never unloaded. Its
  code runs in this process with the process's full access.

`tests/test_host.py` runs the shared host cases (`lib/plugin/testdata/v1/host-cases.json`), the
same file the Go host is held to, on the reference guest as a child process and as a C ABI
library. `scripts/host-cases.sh` builds the engine and both guests and runs it.

