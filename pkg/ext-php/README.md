# ext for PHP (`webong/ext-io`): plugin guest SDK

> **Why `ext-io`:** the bare name `ext` is taken on the registries, so `ext-io` is the one package for all of ext's APIs, one namespace per area. Only `ExtIO\Plugin` is built so far; `ExtIO\System`, `ExtIO\Web` and `ExtIO\Int` are planned.

A guest for `ext.plugin/v1` over JSON lines that needs only `ext-json` (PHP 8.1+), with no
Composer dependencies and no native build.
Hosts use the Go `pkg/ext-go/engine/host/process` backend (or any JSON-line client) to run it.

```php
use ExtIO\Plugin;

Plugin\serveStdio('plugin.json', function (string $contract, string $operation, mixed $payload): mixed {
    if ($operation === 'busy') {
        throw new Plugin\RemoteError('busy', 'try later', 250);
    }
    return $payload;                    // any JSON-encodable value
});
```

The manifest file is `plugin.json`. `serveStdio` accepts the manifest or a bare descriptor file by
structure.

Install it with Composer (`composer require webong/ext-io`, PSR-4 `ExtIO\` → `src/`), or without
Composer `require 'path/to/pkg/ext-php/autoload.php';`. `examples/echo` is a complete guest.

- `serveStdio($descriptor, $handler, $maxCallSeconds = 30.0, $redactErrors = false)`: serves
  until the input ends. `$descriptor` is an array or `stdClass`, or the path of an
  `ext.package/v1` `plugin.json` (its `descriptor` is used) or a bare descriptor file.
- `new Guest($descriptor, $handler, ...)`, `serve($guest, $in, $out)`: the same with
  caller-supplied streams, for tests. `Guest::invoke($request)` answers one decoded request.
- `new RemoteError($code, $message, $retryAfterMs = null)`: sent as the structured error.
  `getCode()` is the string code (as `PDOException`'s is) and `$retryAfterMs` the retry hint.
  Any other `Throwable` becomes `provider_error` with its message cut to 4096 bytes (bytes that
  are not UTF-8 become U+FFFD); that text reaches the host, so pass `redactErrors: true` to send
  a generic `operation_failed` instead.
- `currentCall()`: request id, contract, operation, surface and absolute `deadline` (seconds since
  the epoch) for the in-flight call. Calls are sequential and cannot be interrupted, so long
  handlers should check `remaining()`.
- `loadDescriptor($path)`, `validateDescriptor($d)` (both throw `InvalidArgumentException`).
- `Capability\Router` dispatches `ext.health`, `ext.configuration` and `ext.stream` and checks
  their payloads and results with `Capability\Shape`, as the Python and Go libraries do.

## JSON values

A request is decoded strictly, and its payload reaches the handler as JSON, not as PHP arrays:

- a JSON object is a `stdClass` and a JSON array is a list, so `{}` and `[]` stay apart and a
  guest that echoes its payload returns it unchanged. A handler can return arrays: a list
  encodes as a JSON array and an associative array as an object, but an *empty* PHP array
  encodes as `[]`, so return `new \stdClass()` for an empty object.
- an integer literal is an `int`, or a `JsonInteger` (its exact digits) when it does not fit in
  64 bits, which encodes back to the same digits; a number with a fraction or exponent is a
  `float`, and `1.0` encodes as `1.0`.
- results are encoded with `JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE |
  JSON_PRESERVE_ZERO_FRACTION`. A result that is not JSON (a resource, `NAN`, `INF`, a string
  that is not UTF-8) is a `provider_error`.

## Report progress and call the host

A guest served by `serve` can report progress while a call runs and call back into its host.
An operation that reports declares `"progress": true` in the descriptor; the host operations a
guest may call are the descriptor's `reverse` list. Both go through `currentCall()`:

```php
function handler(string $contract, string $operation, mixed $payload): mixed
{
    $call = Plugin\currentCall();
    // Events reach the host in order, before the result. DROPPED is routine: the rate limit the
    // host set dropped it and counts it for the next event.
    $call->progress(Plugin\Progress::log('starting'));
    $call->progress(Plugin\Progress::fraction(1, 3));
    // The host's refusal or error throws RemoteError with its code (denied, busy, too_large,
    // expired, ...).
    return $call->reverse->call(['host.svc', 'v1'], 'sign', $payload);
}
```

`$call->progress()` throws `FrameError` whose `getCode()` is `unsupported` (the operation did not
declare progress, the host asked for no events, or there is no frame channel: stop reporting),
`invalid` (the event breaks the rules; nothing is sent) or `closed` (the call is over).
`$call->progressLimits` is the rate the host set, and `splitProgressText()` cuts a long line into
events that each fit. This guest reads *inline*: PHP has no threads, so the handler that waits for
the host's answer reads the connection itself. The host sends nothing but that answer during a
call, so this suffices, with limits: one reverse call at a time (`$call->reverse->concurrent` is
false); `$call->sessionReverse` is always `null`; and the wait ends with the answer or the end of
the connection, since standard input cannot be read with a timeout (`timeout:` bounds the deadline
the host is given). A frame the host may not send ends the session with `ProtocolError` and the
call in flight is not answered; when the input ends during a call, the call is answered and the
session ends. `Guest::invoke($request)` on its own has no frames: `$call->reverse` is `null`.

Behavior matches the Go `jsonline` server and the Python guest: `plugin.hello` is required first
and answered with the descriptor; frames are at most 24 MiB and read with a bound; envelopes with
unknown fields, duplicate keys, trailing values, non-finite numbers, unpaired surrogates or
nesting beyond 64 levels end the stream; each request is checked for API version, id, identity,
contract, operation, surface and deadline, and a mismatch returns `invalid_request`.

Standard output carries only protocol frames: `serveStdio` writes them to a private duplicate of
file descriptor 1 and points descriptor 1 at standard error, so `echo`, `print`, warnings and
child processes write there. The `STDOUT` constant is closed: write diagnostics to `STDERR`.
`display_errors` goes to standard error, and a `memory_limit` below 512M is raised to 512M, since
one 24 MiB frame is held several times over.

## Differences from the Python guest

- No threads: a reverse call is made from the handler, so there is no "other thread" refusal.
- A JSON object key that starts with NUL cannot be a PHP property, so a frame that holds one ends
  the stream (no shared fixture uses one).
- `RemoteError` and `FrameError` carry their string code in `getCode()`; a `RemoteError` message
  must be UTF-8.

## Tests

`php tests/run.php` here (no dependencies; CI runs it on PHP 8.1 and the latest release).
`tests/FixtureTest.php` reproduces the shared fixtures
(`lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases,model-cases,invalid}.json`, computed
by the Go contract) with no exceptions, `tests/ServeFramesTest.php` covers the router as a host
on pipes, and `tests/frames_guest.php` is the frames conformance guest of
[docs/plugin-guest-frames.md](https://github.com/webong/ext/blob/main/docs/plugin-guest-frames.md).
The Go suites run this guest through `process.Start`: `TestPHPGuest` in
`pkg/ext-go/engine/plugintest` against the shared conformance cases, and `TestPHPFramesGuest` in
`tests/crosslang` through the frames suite (and the C engine under
`scripts/plugin-crosslang.sh`); both skip when `php` is not installed.
