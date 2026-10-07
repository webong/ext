# CTX plugin SDK for Rust

`ext-plugin` is the Rust host/guest implementation of `ext.plugin/v1`. This
development crate is part of CTX and is not published to crates.io. Use a path
dependency pointing at this directory, or a pinned CTX Git revision. Rust 1.88+
is required by the locked dependencies; CI and local examples use Rust 1.98.1.

## Write a typed guest

```rust
use ext_plugin::{ContractRef, Guest, Identity, Method, Operation, Registry, Result};
use serde::{Deserialize, Serialize};
use std::sync::Arc;

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Message { text: String }

fn guest() -> Result<Guest> {
    let mut registry = Registry::new(Identity {
        id: "example/echo".into(), revision: "build-1".into(), version: "".into(),
    })?;
    let mut echo = Method::<Message, Message>::new(
        ContractRef { name: "example.echo".into(), version: "v1".into() },
        Operation { name: "echo".into(), surface: "observation".into() },
    );
    echo.validate_input = |v| {
        if v.text.len() > 256 { Err(ext_plugin::Error::Invalid) } else { Ok(()) }
    };
    registry.register(echo, |context, _request, input| {
        context.check()?;
        Ok(input)
    })?;
    registry.guest(Arc::new(|request| {
        // Apply your domain authorization here. This local example allows echo.
        if request.operation == "echo" { Ok(()) } else { Err(ext_plugin::Error::Denied) }
    }))
}
```

The registry derives the descriptor and freezes it when creating a guest. Later
registry changes do not expand that guest. Input/output validators apply on
both typed host calls and guest dispatch. Serde handles payload shapes; use
`deny_unknown_fields` on strict domain structs. Validators supply domain bounds.
This crate does not implement the separate `ext.schema/v1` schema interpreter.

## Choose the guest entry point

**Standalone or WASI command:**

```rust,ignore
ext_plugin::guest::serve(&guest()?, std::io::stdin().lock(), std::io::stdout().lock())?;
```

**C shared library:** configure `crate-type = ["cdylib"]`, then export:

```rust,ignore
ext_plugin::export_guest!(guest);
```

The macro supplies the four exports from CTX's [C ABI](../../pkg/plugin-cshared/ext_plugin.h).
Each `open` calls the factory for an independent guest; up to 64 handles are
admitted. Handle removal releases the guest when outstanding references finish.
Guest-owned closure state can use `Arc`/`Drop` for resources. Panics are caught
at the C boundary when the build uses unwinding; `panic=abort` still terminates
the process. Foreign callers must pass valid, non-overlapping owned buffers.
They may not retain pointers, call a closed handle, or unload the library.

## Host a plugin

`host::Session::open(selected, verify, connect, authorize)` requires policy
callbacks. It owns the selection, verifies before connecting, then checks the
entire handshake. `Session::call(&method, input)` reuses the typed method.
`call_with_timeout` supplies a shorter deadline. `call_raw` returns JSON values
for contracts where the host has no typed wrapper. Public errors are
`Error::Remote`; other guest failures become `operation_failed`.

Native host connections:

- `native::Worker::process(path, args)` owns a child speaking CTX JSON lines.
  It inherits the host process environment and stderr. For different launch
  policy or a supplied connection, implement `host::Backend` explicitly.
- `native::Worker::cshared(absolute_path)` loads CTX C ABI v1. Verified immutable
  paths are required; library constructors execute during connection. Images
  are cached by canonical path and remain resident even after ABI failures.
- Custom `host::Backend` implementations receive operation `1` with
  `{"deadline": ...}` for handshake, or operation `2` with the CTX request.
  They return descriptor/response JSON and own transport deadlines and cleanup.

Sessions are serial mutable APIs. Closing/dropping a session closes its backend
once. `close_and_wait(timeout)` additionally waits for worker cleanup with a
caller-supplied bound. Transport errors, malformed responses, mismatch and
dispatch timeout fail the session; authorization denial and intentional remote errors retain it.
Native workers stop waiting on timeout. Process cleanup kills the owned child;
C cleanup runs after outstanding native code returns. Constructor/open calls
are synchronous and cannot be forcibly interrupted. There is no automatic
retry, OS sandbox, process-tree containment or native-code unloading.

Handlers call `CallContext::check()` around long work and honor its deadline.
`serve` takes caller-supplied blocking `BufRead`/`Write`: it cannot interrupt
arbitrary I/O or force a synchronous handler to stop. The Go WASI backend can
terminate the containing module. Native host workers are excluded from WASI;
the guest, wire contract and abstract host Session remain portable.

## Build and test

From the repository root:

```sh
cargo test --locked --manifest-path pkg/plugin-rust/Cargo.toml
cargo build --locked --manifest-path pkg/plugin-rust/Cargo.toml --examples
rustup target add wasm32-wasip1
cargo build --locked --manifest-path pkg/plugin-rust/Cargo.toml --target wasm32-wasip1 --example guest
scripts/plugin-crosslang.sh
```

The examples share one fixture plugin providing echo, wait and deliberate error
operations. `examples/guest.rs` is the command/WASI guest, `shared.rs` is the
dynamic library, and `host.rs` exercises either a C library or a child command.
The cross-language script supplies matching Go guests and verifies both sides.
It requires Zig 0.17.0 too; set `ZIG_BIN` if it is not on PATH.

Wire parsing rejects duplicate keys, unknown envelope fields, invalid UTF-8,
trailing JSON, nesting beyond 64 and frames over 24 MiB. Rust JSON values use
signed/unsigned 64-bit integers and finite floating-point numbers. Use decimal
strings for larger exact numbers and integers outside JavaScript's safe range
when a TypeScript peer is involved. ABI/version compatibility does not supply
domain authorization or imply that arbitrary Rust libraries are CTX plugins.
