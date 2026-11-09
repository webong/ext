# ExtIO.Host

The ext plugin host for .NET: runs `ext.plugin/v1` plugins through the ext C engine (`lib/plugin`),
which does what a host must get right: admission, the handshake check against the descriptor,
request and response validation, deadlines and the session's lifecycle. This package only moves
bytes and policy decisions across P/Invoke. It loads the engine's shared library (`libext_host`,
`ext_host.dll` on Windows, from an `ext-engine-vX.Y.Z` release or a CMake build of `lib/plugin`) at
run time, so it carries no native code of its own. `net8.0`, any OS the engine is built for.

```csharp
using System.Text.Json.Nodes;
using ExtIO.Host;

var engine = new Engine();                      // EXT_HOST_LIBRARY, or libext_host on the loader path
using var host = engine.Process(descriptor, "/opt/plugins/echo/1.0.0/echo");
host.Start();
var result = host.Call("ext.echo", "v1", "echo", new JsonObject { ["value"] = 7 });   // {"value":7}
```

- `new Engine(string? path = null)`: the engine from `path`, `EXT_HOST_LIBRARY`, or `libext_host`
  on the loader path; it must have host ABI 2 (`DllNotFoundException` otherwise). An engine is
  never unloaded.
- `engine.Process(descriptor, executable, arguments, environment, workingDirectory, stderrBytes, verify, authorize)`:
  a JSON-line guest started as `executable` (an absolute path, no shell); `environment` entries are
  `NAME=value`, added to a scrubbed base of `PATH`.
- `engine.SharedLibrary(descriptor, path, verify, authorize)`: a guest library that exports the C
  ABI of `ext_plugin.h`, loaded when the host starts, after `verify`, and never unloaded. Its native
  code runs in this process with its full access.
- `verify` and `authorize` are `Policy` delegates that receive the decoded JSON and return true to
  allow; null allows, and an exception denies. A policy may not use the host it decides for: a call
  to it from inside the policy throws `HostException` `invalid`.
- `Start`, `Call(contractName, contractVersion, operation, payload, timeout, token)` (the payload,
  or null), `Invoke` (UTF-8 envelopes in and out), `Drain`, `Close`, `State`, `Stderr`, `Dispose`.
  Timeouts default to 3 seconds. A `CancellationToken` cancels the call in progress ("canceled").
- A domain error throws `RemoteException` (`ErrorCode`, `Message`, `RetryAfterMilliseconds`); a host
  failure throws `HostException`, whose `Status` is the engine's status by name (`denied`, `closed`,
  `timeout` ...).

Calls may come from several threads; the engine serializes a host's calls for a process or a
shared library. The engine runs policies on the thread making the call, so they are
`UnmanagedCallersOnly` callbacks on that thread. `Dispose` closes the session, waits for the calls
in flight to return, then frees the host; an unreachable host is freed by its finalizer.

The engine's functions are resolved with `NativeLibrary` as unmanaged function pointers rather than
`LibraryImport`, so each `Engine` binds the library it was given. The structures restate
`ext_host.h`; the ABI version check and the shared host cases
(`lib/plugin/testdata/v1/host-cases.json`) hold them to it.

## Tests

`ExtIO.Host.Tests` runs every host case on the reference guest as a process and as a C ABI
library, then threads, cancellation, policies, envelopes and ownership:

    dotnet run --project pkg/ext-dotnet/ExtIO.Host.Tests -- lib/plugin/testdata/v1

It needs `EXT_HOST_LIBRARY`, `EXT_GO_GUEST` and `EXT_GO_SHARED`; without them it says so and exits
0 (1 with `EXT_HOST_REQUIRED=1`). `scripts/host-cases.sh` builds them all and runs it on Linux,
macOS and Windows.
