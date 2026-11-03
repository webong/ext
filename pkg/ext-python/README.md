# ext for Python (`ext-io`): plugin guest SDK

> **Why `ext-io`:** the bare name `ext` is taken on crates.io, PyPI and npm, so `ext-io` is the one package for all of ext's APIs, one namespace per area. Only `plugin` is built so far; `system`, `web` and `int` are planned.

Standard-library-only guest for `ext.plugin/v1` over JSON lines (Python 3.8+).
Hosts use the Go `pkg/ext-go/engine/host/process` backend (or any JSON-line client) to run it.

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
