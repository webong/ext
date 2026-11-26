# ext for Ruby (`ext-io`): plugin guest SDK

> **Why `ext-io`:** the bare name `ext` is taken on RubyGems (and on crates.io, PyPI and npm), so `ext-io` is the one gem for all of ext's APIs, one namespace per area. Only `plugin` is built so far; `system`, `web` and `int` are planned.

Standard-library-only guest for `ext.io.pc/v1` over JSON lines (Ruby 3.1+). It needs no C and no
native build. Hosts use the Go `pkg/ext-go/engine/plugin/host/process` backend (or any JSON-line client) to run it.
A Ruby program can also host plugins itself with the opt-in [host SDK](#host-sdk-extiohost) on the C engine.

```ruby
require "ext_io/plugin"

ExtIO::Plugin.serve_stdio("plugin.json") do |contract, operation, payload|   # or a descriptor Hash
  raise ExtIO::Plugin::RemoteError.new("busy", "try later", retry_after_ms: 250) if operation == "busy"

  payload                      # any JSON value: Hash, Array, String, Symbol, Integer, Float, true, false, nil
end
```

The example uses `plugin.json` for an `ext.io/v1` manifest; callers choose the filename. `serve_stdio` accepts the
manifest or a bare descriptor file by structure. [`examples/echo`](../../examples/plugins/ruby/echo) is a complete
plugin: a manifest whose artifact is `main.rb`, served through `Capability::Router`.

Add `lib` to the load path (`ruby -I pkg/ext-ruby/lib`), or build and install the gem
(`gem build ext-io.gemspec`). Requiring `"ext_io"` alone loads no area.

- `ExtIO::Plugin.serve_stdio(descriptor, handler = nil, max_call_seconds: 30, redact_errors: false, &block)`:
  serves until EOF. `descriptor` is a Hash or the path of an `ext.io/v1` `plugin.json` (its
  runtime descriptor is derived from identity and contracts) or a bare descriptor file. The handler is the second argument or the
  block: anything that responds to `call(contract, operation, payload)`.
- `Guest.new(descriptor, handler = nil, ...)`, `serve(guest, input, output)`: the same with
  caller-supplied binary streams (a `StringIO` works), for tests. `Guest#invoke(request)` answers
  one decoded request.
- `RemoteError.new(code, message, retry_after_ms: nil)`: sent as the structured error. Any other
  error (a `StandardError`, a `ScriptError` such as `NotImplementedError`, or a
  `SystemStackError`) becomes `provider_error` with its message cut to 4096 bytes; that text
  reaches the host, so pass `redact_errors: true` to send a generic `operation_failed` instead.
  Signals, `exit` and `NoMemoryError` still end the guest.
- `ExtIO::Plugin.current_call`: request id, contract, operation, surface and absolute `deadline`
  (a `Time`) for the in-flight call. Calls are sequential and cannot be interrupted, so long
  handlers should check `remaining` (seconds, a Float).
- `load_descriptor(path)`, `validate_descriptor(hash)`: both raise `ArgumentError`. A descriptor
  given as a Hash is read as JSON first, so symbol keys work.
- `Capability::Router` with `health`, `configuration`, `stream(open:, read:, close:)` and `on`:
  the typed `ext.health`, `ext.configuration` and `ext.stream` contracts, checked before and after
  your code runs (a payload that fails is `invalid_input`).

## Report progress and call the host

A guest served by `serve` can report progress while a call runs and call back into its host.
An operation that reports declares `"progress": true` in the descriptor; the host operations a
guest may call are the descriptor's `reverse` list. Both go through `current_call`:

```ruby
handler = lambda do |contract, operation, payload|
  call = ExtIO::Plugin.current_call
  # Events reach the host in order, before the result. DROPPED is routine: the rate limit the
  # host set dropped it and counts it for the next event.
  call.progress(ExtIO::Plugin::Progress.log("starting"))
  call.progress(ExtIO::Plugin::Progress.fraction(1, 3))
  # The host's refusal or error raises RemoteError with its code (denied, busy, too_large,
  # expired, ...).
  call.reverse.call(["host.svc", "v1"], "sign", payload)
end
```

`call.progress` returns `ExtIO::Plugin::SENT` or `DROPPED` and raises `FrameError` whose `code` is
`unsupported` (the operation did not declare progress, the host asked for no events, or there is
no frame channel: stop reporting), `invalid` (the event breaks the rules; nothing is sent) or
`closed` (the call is over). `call.progress_limits` is the rate the host set, and
`ExtIO::Plugin.split_progress_text` cuts a long line into events that each fit. This guest reads
*inline*: it has no reader of its own, so the thread that waits for the host's answer reads the
connection itself. The host sends nothing but that answer during a call, so this suffices, with
limits: one reverse call at a time, from the handler's thread (`call.reverse.concurrent` is false
and another thread gets `unsupported`); `call.session_reverse` is always `nil`; and the wait ends
with the answer or the end of the connection, since standard input cannot be read with a timeout
(`timeout:` bounds the deadline the host is given). A frame the host may not send ends the
session with `ProtocolError` and the call in flight is not answered; when the input ends during a
call, the call is answered and the session ends. `Guest#invoke(request)` on its own has no frames:
`call.reverse` is `nil`.

Behavior matches the Go `jsonline` server and the Python SDK (`pkg/ext-python`): `session.hello`
is required first and answered with the descriptor; frames are at most 24 MiB and read with a
bound; envelopes with unknown fields, duplicate keys, trailing values, non-finite numbers,
unpaired surrogates or nesting beyond 64 levels end the stream; each request is checked for API
version, id, identity, contract, operation, surface and deadline, and a mismatch returns
`invalid_request`. The JSON reader and writer are the SDK's own (the `json` gem is not used):
frames are written compactly with everything outside printable ASCII escaped, byte for byte as
the Python SDK writes them. Standard output carries only protocol frames: file descriptor 1 (and
so `$stdout`, `STDOUT` and child processes) is redirected to standard error.

## Host SDK (`ExtIO::Host`)

`ExtIO::Host` (`require "ext_io/host"`) runs plugins from Ruby through the C engine (`lib/plugin`),
which does what a host must get right: admission, the handshake check against the descriptor,
request and response validation, deadlines and the session's lifecycle. It is opt-in and separate
from the guest, as the gem `ext-io-host` (`ext-io-host.gemspec`): one native extension,
`ExtIO::Host::Native` (`ext/ext_io_host`), which loads the engine's shared library (`libext_plugin_host`,
from an `ext-plugin-vX.Y.Z` release or a CMake build of `lib/plugin`) at run time, so it is built
against the Ruby headers and `ext_plugin_host.h` only. Outside a checkout, install it with
`gem install ext-io-host -- --with-ext_plugin_host-dir=<unpacked ext-plugin release>` (or set
`EXT_HOST_INCLUDE`). `scripts/ext-ruby-host.sh <dir>` builds the extension into
`<dir>/ext_io/host/native.<so|bundle>` for `-I <dir>`. Requiring `"ext_io/plugin"` loads neither.

```ruby
require "ext_io/host"

engine = ExtIO::Host::Engine.new        # EXT_HOST_LIBRARY, or libext_plugin_host on the loader path
engine.process(descriptor, "/opt/plugins/echo/1.0.0/echo") do |host|   # destroyed after the block
  host.start
  host.call({ "name" => "ext.echo", "version" => "v1" }, "echo", { "value" => 7 })  # => {"value"=>7}
end
```

- `Engine.new(path = nil)`: the engine from `path`, `EXT_HOST_LIBRARY`, or `libext_plugin_host` on the
  loader path; it must have host ABI 2 (`LoadError` otherwise). An engine is never unloaded.
- `engine.process(descriptor, executable, arguments:, environment:, working_directory:, stderr_bytes:, verify:, authorize:)`:
  a JSON-line guest started as `executable` (an absolute path, no shell); `environment` entries are
  `NAME=value`, added to a scrubbed base of `PATH`.
- `engine.shared_library(descriptor, path, verify:, authorize:)`: a guest library that exports the C
  ABI of `ext_plugin_guest.h`, loaded when the host starts, after `verify`, and never unloaded. Its
  native code runs in this process with its full access.
- `verify` and `authorize` are callables that receive the decoded JSON and return truthy to allow;
  absent allows, and an exception denies. A policy may not use the host it decides for: a call to
  it from inside the policy raises `HostError` `invalid`.
- `start(timeout = 3.0)`, `call(contract, operation, payload = nil, timeout: 3.0)` (the payload),
  `invoke(envelope, timeout: 3.0)` (JSON strings in and out), `drain(timeout)`, `close`, `state`,
  `stderr`, `destroy`, `destroyed?`. With a block, `process` and `shared_library` yield the host and
  destroy it afterwards; garbage collection frees an unreachable one.
- A domain error raises `ExtIO::Plugin::RemoteError` (also `ExtIO::Host::RemoteError`); a host
  failure raises `ExtIO::Host::HostError`, whose `status` is the engine's status by name
  (`denied`, `closed`, `timeout` ...).

Every call that can block runs on a native helper thread while the calling Ruby thread waits
without the global VM lock, so other threads keep running and may call the same host; the engine
serializes a host's calls itself. An interrupt that raises (`Thread#raise`, `Thread#kill`, Ctrl-C)
cancels the call in progress through the engine, and is raised once the call has ended; one that
does not, such as a signal trap or a child process ending, runs and the call goes on. The engine
runs policies on the thread making the call, the helper, which hands each to the waiting Ruby
thread to run with the lock.

Reverse calls and the guest registry run the hooks the same way, in the engine's caller-thread mode
(`ext_plugin_host_set_reverse_mode`): a hook runs on the Ruby thread of a call that waits on the host,
never on an engine thread.

- `host.serve(authorize:, handle:, timeout:, max_payload_bytes:, max_in_flight:)`, before `start`,
  offers the plugin the operations its descriptor lists under `reverse`. The hooks get an
  `ExtIO::Host::ReverseCall` (`request`, `remaining`); `handle` returns a JSON value, or raises
  `RemoteError` for an error the plugin receives unchanged. A request made between calls waits for
  the next call or `host.serve_reverse(timeout)`, which returns true once it served one.
- `ExtIO::Host::Registry.new(engine)` composes guests: `host.attach(registry, authorize:)` (in place
  of `serve`) makes a host a source whose plugin's reverse calls are served by forward calls to the
  hosts `registry.register(contract, host)` names, along the edges `registry.route(from, to)`
  declares. Undeclared edges are `reverse_undeclared`, unregistered targets `reverse_unavailable`,
  and a call that would re-enter a host on its call chain `denied`. A registry keeps its targets
  alive, and a destroyed host leaves it.

## Tests

`ruby test/all.rb` here (Minitest, bundled with Ruby) runs:

- `test/test_fixtures.rb`: the shared fixtures
  (`lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases,model-cases}.json`, computed by the
  Go contract), with no exceptions;
- `test/test_guest.rb`, `test/test_capability.rb`, `test/test_serve_frames.rb`: the cases of the
  Python SDK's tests, the router driven by a host on pipes;
- `test/test_example.rb`: the example plugin and its manifest digest;
- `test/test_host.rb`: the host SDK, with every case of `lib/plugin/testdata/v1/host-cases.json`
  on the reference guest as a process and as a C ABI library, then threads, interrupts, policies,
  envelopes and ownership. It needs the extension on the load path, `EXT_HOST_LIBRARY`,
  `EXT_GO_GUEST` and `EXT_GO_SHARED`, and is skipped without them (it fails instead with
  `EXT_HOST_REQUIRED=1`); `scripts/host-cases.sh` builds them all and runs it, on Ruby 3.1, 3.4
  and 4.0 in CI (the `host-cases` area of `.github/ci/areas.json`).

`test/frames_guest.rb` is the frames conformance guest of
[docs/plugin-guest-frames.md](https://github.com/webong/ext/blob/main/docs/plugin-guest-frames.md)
(it answers the operations that need a reader of its own with the public error `unsupported`).
The Go suite in `pkg/ext-go/engine/plugin/plugintest` (`TestRubyGuest`, `TestRubyUnitTests`) runs
`test/conformance_guest.rb` through `process.Start` against the shared conformance fixtures; set
`EXT_RUBY` to an interpreter's absolute path to choose one. CI (the `ext-ruby` area of `.github/ci/areas.json`) runs
all of this on Ruby 3.1 through the latest.

### Coordinating package writes

`Engine#package_writer` holds the C engine's cooperating writer for an Ext home:

```ruby
engine = ExtIO::Host::Engine.new
result = engine.package_writer("/path/to/ext-home") do
  # Plan, publish and update approval in this scope.
  :done
end
```

The block result is returned, and `ensure` closes the native handle even when
the block raises. Without a block, the returned handle must be closed explicitly
with `close` (idempotent). Pass the Ext home, not its `packages` subdirectory.
The C engine uses the same `.package-write.lock` directory as the CLI and CTX.
Contention and stale directories raise `HostError` with status `exists`; locks
are never stolen. Releasing a replacement owned by another writer fails with
`mismatch` without removing it. An abrupt exit leaves a stale lock: establish
that the former owner stopped before manual recovery. Store/trust calls do not
lock implicitly. This requires a library exposing the `package_writer` object;
older engines without the described interface report `unsupported` on this API.
The host conformance suite includes block cleanup, contention, replacement
ownership and abrupt-child-exit checks.
