# ext for Lua (`ext-io`): plugin guest SDK

> **Why `ext-io`:** the bare name `ext` is taken on crates.io, PyPI and npm, so `ext-io` is the one package for all of ext's APIs, one namespace per area. Only `plugin` is built so far; `system`, `web` and `int` are planned.

Pure-Lua guest for `ext.plugin/v1` over JSON lines: no C modules and no native build, on Lua 5.1 to
5.5 and LuaJIT. It brings its own strict JSON module (`ext_io.json`). Hosts use the Go
`pkg/ext-go/engine/host/process` backend (or any JSON-line client) to run it. A Lua program can
also host plugins itself with the opt-in [host SDK](#host-sdk-ext_iohost) on the C engine.

```lua
local plugin = require("ext_io.plugin")

local function handler(contract, operation, payload)
  if operation == "busy" then
    error(plugin.remote_error("busy", "try later", 250))
  end
  return payload                        -- any JSON value; nil is null
end

plugin.serve_stdio("plugin.json", handler)   -- or a descriptor table
```

The manifest file is `plugin.json`. `serve_stdio` accepts the manifest or a bare descriptor file by
structure.

Put this directory on `package.path` (`?.lua` and `?/init.lua`), or `luarocks make` the rockspec
here. [`examples/echo`](examples/echo) is a complete guest.

- `serve_stdio(descriptor, handler, opts)`: serves until the input ends. `descriptor` is a table or
  the path of an `ext.package/v1` `plugin.json` (its `descriptor` is used) or a bare descriptor
  file. `opts` holds `max_call_seconds` (default 30), `redact_errors` (default false) and `clock`
  (see [Time](#time)).
- `Guest(descriptor, handler, opts)` (or `Guest.new`), `serve(guest, input, output)`: the same with
  caller-supplied streams, for tests. `input` has a `read` method like a Lua file and `output` has
  `write` and `flush`; `guest:invoke(request)` validates and answers one decoded request.
- `remote_error(code, message, retry_after_ms)`: a structured error; raise it with `error()`
  (`is_remote_error` tells one apart). Any other error becomes `provider_error` with
  `tostring(err)` cut to 4096 bytes on a character boundary (bytes that are not UTF-8 become `?`);
  that text reaches the host, so pass `redact_errors = true` to send a generic `operation_failed`
  instead. A string error raised with `error(msg)` carries Lua's `file:line:` prefix; use
  `error(msg, 0)` to leave it out.
- `current_call()`: `request_id`, `contract_name`, `contract_version`, `operation`, `surface` and
  absolute `deadline` (epoch seconds) of the in-flight call. Calls are sequential and cannot be
  interrupted, so long handlers should check `call:remaining()`.
- `load_descriptor(path)`, `validate_descriptor(d)`: raise a string error for an invalid descriptor.

## Values

`ext_io.json` maps JSON to Lua like this, and the handler sees payloads in this form:

| JSON | Lua |
| --- | --- |
| object | table with metatable `json.object_mt`; the decoded key order is kept and written back |
| array | table with metatable `json.array_mt`, elements `1..n` |
| null | the sentinel `json.null` (also `plugin.null`) inside tables; a null or absent top-level payload is `nil` |
| number | an integer when the text has no fraction or exponent and it fits (Lua 5.3+), else a float; Lua 5.1, 5.2 and LuaJIT have only floats |
| string, true, false | string (UTF-8), booleans |

When encoding a result, a plain table is an array when its keys are exactly `1..n` and an object when
they are all strings. **An empty plain table is an object, `{}`**: return `json.array()` (or
`json.array(t)`) for an empty array, and `json.object(t, keys)` to fix the key order (plain tables
are written with sorted keys). A table that mixes keys, has holes, a cycle, a non-finite number, a
function or a string that is not UTF-8 cannot be encoded and becomes a `provider_error`.

Decoding is strict, as the Go host's: UTF-8 only, no duplicate keys, trailing data, `NaN`/`Infinity`,
leading zeros, raw control characters, invalid escapes or unpaired surrogate escapes (a valid pair
decodes to its four-byte character). Output is ASCII: every character past U+007E is a `\u`
escape, as Python's `ensure_ascii` writes it. `json.number_text(container, key)` returns the
source text of a number whose value alone loses information (a fraction or exponent, sixteen or
more characters, or `-0`); the frame checks use it, so `1.0` is not a whole number and counts up
to 2^64 - 1 compare exactly on every Lua. A decoded number written back unchanged keeps what its
text said, so an echoed payload is the same bytes as from the Python SDK on every Lua, integers past
2^53 included.

## Report progress and call the host

A guest served by `serve` can report progress while a call runs and call back into its host. An
operation that reports declares `"progress": true` in the descriptor; the host operations a guest
may call are the descriptor's `reverse` list. Both go through `current_call()`:

```lua
local function handler(contract, operation, payload)
  local call = plugin.current_call()
  -- Events reach the host in order, before the result. DROPPED is routine: the rate limit the
  -- host set dropped it and counts it for the next event.
  call:progress(plugin.Progress.log("starting"))
  call:progress(plugin.Progress.fraction(1, 3))
  -- The host's refusal or error raises a RemoteError with its code (denied, busy, too_large,
  -- expired, ...).
  return call.reverse:call({ "host.svc", "v1" }, "sign", payload)
end
```

`call:progress` raises a FrameError (`is_frame_error`) whose `code` is `unsupported` (the operation
did not declare progress, the host asked for no events, or there is no frame channel: stop
reporting), `invalid` (the event breaks the rules; nothing is sent) or `closed` (the call is over).
Events are `Progress.log(text)`, `Progress.out(text)`, `Progress.err(text)`,
`Progress.fraction(completed, total)` or `Progress(kind, stream, text, completed, total)`.
`call.progress_limits` is the rate the host set (`events_per_second`, `bytes_per_second`), and
`split_progress_text` cuts a long line into events that each fit. This guest reads *inline*: it
has no reader of its own, so the code that waits for the host's answer reads the connection
itself. The host sends nothing but that answer during a call, so this suffices, with limits: one
reverse call at a time (`call.reverse.concurrent` is false); `call.session_reverse` is always nil;
and the wait ends with the answer or the end of the connection, since standard input cannot be read
with a timeout (`call.reverse:call(contract, operation, payload, timeout)` bounds the deadline the
host is given). A frame the host may not send ends the session with a ProtocolError
(`is_protocol_error`) and the call in flight is not answered; when the input ends during a call,
the call is answered and the session ends. `guest:invoke(request)` on its own has no frames:
`call.reverse` is nil.

`plugin.capability.Router()` is a handler for the optional `ext.health`, `ext.configuration` and
`ext.stream` contracts that checks each payload and result against them (`router:health(check)`,
`router:configuration(update)`, `router:stream(open, read, close)`, `router:on(...)`).

## Time

Deadlines are epoch seconds on the wall clock. Standard Lua has no sub-second wall clock and this
package loads no C module, so `ext_io.plugin.clock` picks its source once: on LuaJIT the built-in
FFI calls `clock_gettime` (`GetSystemTimeAsFileTime` and `QueryPerformanceCounter` on Windows);
elsewhere it is `os.time()`, whole seconds, so a deadline is honoured to within a second and the
progress rate limit refills once a second. `plugin.clock.source` says which. A guest with a better
clock passes it: `{ clock = require("socket").gettime }` (LuaSocket) or any function returning
epoch seconds.

## Differences from the Python SDK

- Options are a table (`opts`), errors are raised with `error()` and told apart with
  `is_remote_error`, `is_frame_error` and `is_protocol_error`, and validation failures are string
  errors where Python raises `ValueError`.
- A reverse call may come from any coroutine of the handler: Lua has no threads, so the
  "another thread gets `unsupported`" rule has nothing to guard.
- `serve_stdio` sends `print`, `io.write` and `io.stdout` to standard error while it serves, but
  pure Lua cannot duplicate file descriptors: a child process started with `os.execute` or
  `io.popen` still writes to the real standard output. On Windows the standard streams are in text
  mode, which pure Lua cannot change.
- On Lua 5.2+ and LuaJIT a frame is read as one line and then checked against the 24 MiB limit;
  Lua 5.1 has no `"*L"` read format, so it reads byte by byte, which is bounded but slower.

## Host SDK (`ext_io.host`)

`ext_io.host` runs plugins from Lua through the C engine (`lib/plugin`), which does what a host must
get right: admission, the handshake check against the descriptor, request and response validation,
deadlines and the session's lifecycle. It is opt-in and separate from the guest: one C module,
`ext_io.host.core`, which loads the engine's shared library (`libext_host`, from an `ext-engine-vX.Y.Z`
release or a CMake build of `lib/plugin`) at run time, so it is built against the Lua headers and
`ext_host.h` only. Requiring `ext_io.plugin` loads neither. The rock is `ext-io-host`
(`ext-io-host-0.1.0-1.rockspec`, built from a checkout); `scripts/ext-lua-host.sh <dir>` builds the
module for one interpreter into `<dir>/ext_io/host/core.so` (`core.dll` on Windows, where it links
against the interpreter's Lua library and is built by gcc from MSYS2 or, with `CC=cl`, by MSVC).

```lua
local host = require("ext_io.host")

local engine = host.Engine()          -- EXT_HOST_LIBRARY, or libext_host on the loader path
local h <close> = engine:process(descriptor, "/opt/plugins/echo/1.0.0/echo")   -- Lua 5.4+
h:start()
print(h:call({ name = "ext.echo", version = "v1" }, "echo", { value = 7 }).value)
```

- `Engine(path)`: the engine from `path`, `EXT_HOST_LIBRARY`, or `libext_host` on the loader path; it
  must have host ABI 2. An engine is never unloaded.
- `engine:process(descriptor, executable, opts)`: a JSON-line guest started as `executable` (an
  absolute path, no shell). `opts` holds `arguments`, `environment` (`NAME=value` entries added to a
  scrubbed base of `PATH`), `working_directory`, `stderr_bytes` (default 4096) and the policies.
- `engine:shared_library(descriptor, path, opts)`: a guest library that exports the C ABI of
  `ext_plugin.h`, loaded when the host starts, after `verify`, and never unloaded. Its native code
  runs in this process with its full access.
- `opts.verify(descriptor)` and `opts.authorize(request)` receive decoded JSON and return true to
  allow; absent allows. An error denies, and so does a yield.
- `h:start(timeout)`, `h:call(contract, operation, payload, timeout)` (the payload, or `nil`),
  `h:invoke(envelope, timeout)` (JSON strings in and out), `h:drain(timeout)`, `h:close()`,
  `h:state()`, `h:stderr()` and `h:destroy()`; timeouts are seconds (default 3). Garbage collection
  and `<close>` destroy a host too.
- A domain error raises the guest SDK's remote error (`host.is_remote_error`, with `code`, `message`
  and `retry_after_ms`); a host failure raises a `HostError` (`host.is_host_error`) whose `status` is
  the engine's status by name (`denied`, `closed`, `timeout` ...).

The engine runs policies on the thread that makes the call, which is the Lua thread (or coroutine)
calling `start` or `call`, so the module calls them there with `lua_pcall`. A policy may not use the
host it decides for: a call to it from inside the policy raises `HostError` `invalid`. Lua has no
threads, so calls are sequential; the engine still serializes a host's calls itself.

## Behavior

Behavior matches the Go `jsonline` server: `plugin.hello` is required first and answered with the
descriptor; frames are at most 24 MiB; envelopes with unknown fields, duplicate keys, trailing
values, non-finite numbers, unpaired surrogates or nesting beyond 64 levels end the stream; each
request is checked for API version, id, identity, contract, operation, surface and deadline, and a
mismatch returns `invalid_request`. Frames are written byte for byte as the Python SDK writes them.

## Tests

`lua tests/run.lua` here (no dependencies; `lua tests/run.lua test_guest` runs one file). The tests
reproduce the shared fixtures (`lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases,model-cases}.json`,
computed by the Go contract) with no exceptions, and cover the router with a scripted host in a
coroutine. `scripts/ext-lua.sh` (with `EXT_LUA` naming the interpreter) runs them, then
`TestLuaGuest` in `pkg/ext-go/engine/plugintest`, which runs `tests/conformance_guest.lua` through
`process.Start` against the shared conformance suite, and `TestLuaFramesGuest` in
`tests/crosslang`, which runs `tests/frames_guest.lua`, the frames conformance guest of
[docs/plugin-guest-frames.md](https://github.com/webong/ext/blob/main/docs/plugin-guest-frames.md)
(it answers the operations that need a reader of its own with the public error `unsupported`).
`.github/workflows/ext-lua.yml` runs that script on Lua 5.1, 5.2, 5.3, 5.4, 5.5 and LuaJIT.

`tests/test_host.lua` checks the host SDK: every case of `lib/plugin/testdata/v1/host-cases.json` on
the reference guest as a process and as a C ABI library, then policies, envelopes, reentry and
ownership. It needs the C module on `package.cpath`, `EXT_HOST_LIBRARY`, `EXT_GO_GUEST` and
`EXT_GO_SHARED`, and is skipped without them (it fails instead with `EXT_HOST_REQUIRED=1`).
`scripts/host-cases.sh` builds them all and runs it, and `.github/workflows/host-cases.yml` runs that
on Lua 5.1, 5.4, 5.5 and LuaJIT.
