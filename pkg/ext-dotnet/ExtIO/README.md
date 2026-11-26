# ExtIO

The ext plugin guest SDK for .NET. A plugin written with it speaks `ext.io.pc/v1` as bounded JSON
lines on standard input and output, so any ext host (Go, the C engine, Rust, Zig, TypeScript, Python,
Ruby, PHP, Lua or .NET's own `ExtIO.Host`) can run it as a process. It uses the base class library only
(`System.Text.Json.Nodes` for values) and needs no native code.

```csharp
using System.Text.Json.Nodes;
using ExtIO.Plugin;

Guest.ServeStdio("plugin.json", (contract, operation, payload) => operation switch
{
    "echo" => payload,
    "busy" => throw new RemoteError("busy", "try later", retryAfterMs: 250),
    _ => throw new RemoteError("unsupported", "operation is not implemented"),
});
```

`Guest` answers `session.hello` with the descriptor (from a `plugin.json` package manifest or a bare
descriptor), validates every request the way the Go host does, and calls the handler once at a time.

- **Errors.** A `RemoteError` reaches the host as it is. Any other exception becomes `provider_error`
  with its message cut to 4096 bytes; pass `redactErrors: true` to send a generic `operation_failed`
  instead.
- **Call details.** `Guest.CurrentCall` gives the request id, the contract and its version, the
  surface and the deadline.
- **Progress.** `CurrentCall.Progress(Progress.Fraction(1, 3))` or `Progress.Out("line")` reports
  events while the call runs, within the rate the host set. This works only for operations the
  descriptor marks `"progress": true`.
- **Reverse calls.** `CurrentCall.Reverse.Call(new ContractRef("host.svc", "v1"), "sign", payload)`
  calls an operation the host offers, which the descriptor must list under `reverse`. The guest
  reads inline, so a handler makes one reverse call at a time, from its own thread.
- **Capabilities.** `Capability.Router` serves `ext.health`, `ext.configuration` and `ext.stream`.
  It checks each payload before your code runs and each result after.
- **Standard output.** `ServeStdio` sends `Console.Out` to standard error, so stray
  `Console.WriteLine` output cannot corrupt the protocol stream.

The rules are checked against the shared fixtures in `lib/plugin/testdata/v1` (frames, the progress
rate limiter and the wire model) by `ExtIO.Tests`. The Go conformance and frames suites run the guest
in `ExtIO.Conformance` (`scripts/lang/csharp.sh test`).
