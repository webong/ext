# ext_plugin (Python guest SDK)

Standard-library-only guest for `ext.plugin/v1` over JSON lines (Python 3.8+).
Hosts use the Go `pkg/plugin/process` backend (or any JSON-line client) to run it.

```python
import ext_plugin

def handler(contract, operation, payload):
    if operation == "busy":
        raise ext_plugin.RemoteError("busy", "try later", retry_after_ms=250)
    return payload                      # any JSON-serializable value

ext_plugin.serve_stdio("package.json", handler)   # or a descriptor dict
```

Import `ext_plugin` from this directory (add it to `PYTHONPATH`, or `pip install` it).

- `serve_stdio(descriptor, handler, max_call_seconds=30, redact_errors=False)`:
  serves until EOF. `descriptor` is a dict or the path of an `ext.package/v1`
  `package.json` (its `descriptor` is used) or a bare descriptor file.
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

Behavior matches the Go `jsonline` server: `plugin.hello` is required first and
answered with the descriptor; frames are at most 24 MiB and read with a bound;
envelopes with unknown fields, duplicate keys, trailing values, non-finite
numbers, unpaired surrogates or nesting beyond 64 levels end the stream; each
request is checked for API version, id, identity, contract, operation, surface
and deadline, and a mismatch returns `invalid_request`. Stdout carries only
protocol frames: file descriptor 1 and `sys.stdout` are redirected to stderr.

Tests: `python3 -I -m unittest discover -s tests` here, and the Go suite in
`pkg/plugin/plugintest` (`TestPythonGuest`) runs this guest through
`process.Start` against the shared conformance fixtures.
