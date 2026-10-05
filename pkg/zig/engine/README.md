# Zig binding to the CTX C engine

Build with Zig 0.17.0 and a prebuilt C engine:

```sh
zig build --build-file pkg/zig/engine/build.zig \
  -Dengine-include=/absolute/ctx/pkg/plugin/cengine/include \
  -Dengine-lib=/absolute/cengine/build test
```

Add `-Dshared=true` for optional shared linkage. The public module is
`ctx_plugin_engine`. Its `ffi` module is generated from all four C headers by
`translate-c`, so foreign layouts and resource APIs are not manually duplicated.
`Host`, `Guest`, `Instances`, `Lease`, `Streams`, `Cancellation`, `Buffer`, and service helpers provide the
common ownership operations. Call `deinit` exactly once after concurrent uses
have joined; owned values must not be copied. Callbacks and their user data must
outlive handles and must not reenter the same host.

The guest handler uses the C copying result sink. It must cooperate with its
remaining time and must not retain borrowed pointers. Instance/stream APIs are
available through both ownership wrappers and `ffi`. Release all leases and join
calls before destroying a manager. Failed instance close can be retried; failed
`deinit` retains ownership. `Host.setHooks` installs contextual policy/observation. No Go runtime
is linked. The engine currently supports Linux/macOS and is not yet the default
implementation of the parent Zig SDK.
