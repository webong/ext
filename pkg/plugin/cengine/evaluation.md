# C host prototype evaluation

## Decision

**The embedding architecture works as an optional implementation alongside Go.**

One C shared library hosted existing JSON-line guests from Go, Rust, Zig and
Node.js applications. Cgo links that same engine; Rust/Zig do not embed Go.
The Node integration keeps guest work off the event-loop thread. Existing Go,
Rust and Zig command guests passed the C engine's backend conformance harness.

The local checks passed on an Apple M1 running macOS arm64 on 2026-10-05. Linux/macOS CI is configured;
this report does not claim a Linux run. The native implementation has passed
address, undefined-behavior and thread sanitizer checks for the supplied C
lifecycle/concurrency scenarios, plus Go race tests for the wrapper. These are
limited tests, not a comprehensive security or compatibility audit.

## Embedding distribution update

Static linking is now the default for consumers choosing the C implementation;
shared linkage remains optional. Both use the same source and embedding ABI 2,
which separates host policy from backend configuration. Go's independent engine
remains available. The measurements below were collected with the earlier
shared build and are not a static-versus-shared performance comparison.

## Performance evidence

[Raw measurements](bench/results/macos-arm64.jsonl) and the
[complete table/method](bench/results/macos-arm64.md) are retained. Three fresh
process runs per shape, ten warmup calls, one persistent Go echo guest per host.
The C library is a release build; the baseline is the existing Go Session and
JSON-line backend. No race/sanitizer instrumentation is used for measurements.

| Workload | Go | C through cgo | Observation |
| --- | ---: | ---: | --- |
| 16-byte echo, median call latency | 31.9 µs | 25.2 µs | C path lower in this workload |
| 1 KiB echo, median call latency | 70.8 µs | 48.5 µs | C path lower |
| 1 MiB echo, median call latency | 34.95 ms | 24.23 ms | C path lower |
| 1 MiB workload, peak host RSS | 24.6 MiB | 49.7 MiB | C path uses roughly twice the peak memory |
| 8 callers, 1 KiB, p95 latency | 745 µs | 1,434 µs | C path has worse tail latency |
| 8 callers, 1 KiB, throughput | 13,198 calls/s | 16,562 calls/s | Higher throughput does not imply better fairness |

Startup medians across these shapes were 2.63–2.82 ms for C and 3.08–3.37 ms
for Go. These include child launch and handshake. Tiny startup samples are
particularly sensitive to scheduling, process launch and warm filesystem caches.

This comparison measures entire implementations, including different JSON
parsers, buffer allocation, repeated validation and synchronization. It does
not isolate cgo overhead or prove C is intrinsically faster. Go allocation
metrics exclude native allocations; host peak RSS includes them. Guest memory
is excluded. RSS is a high-water mark, not a leak or retained-memory measure.
Foreign bindings are interoperability checks, not a Rust/Zig/Node performance
comparison. Streaming, WASM, long-running services and multiple sessions were
not benchmarked. Results should not be generalized to those workloads.

## What the prototype establishes

- A versioned C boundary can expose CTX host behavior without embedding Go in
  the engine. A Go-written guest still has its own runtime in its subprocess.
- Verification-before-execution, authorization, exact declaration matching,
  bounded wire validation, response correlation and abortive cleanup can be
  implemented centrally and reused across language bindings.
- The existing guest protocol allows reuse of guests without rewriting them.
- Shared-library distribution is practical for the tested local architecture;
  CMake installs the header, native library and dependency license.

## What must improve before broader C adoption

1. **Allocation and admission behavior:** profile the higher native peak memory
   and the queue's worse p95 under contention. Admission currently has no FIFO
   guarantee. Measure sustained mixed workloads and retained memory.
2. **API hardening:** review ownership, callback reentrancy, deadline precision,
   cancellation, destruction and allocation failure. Add fuzzing and differential
   wire-contract tests before declaring this a stable ABI.
3. **Platform and packaging coverage:** exercise Linux CI, design Windows process
   I/O, establish binary release packaging and library search-path rules.
4. **Real consumer integration:** replace the fixture-specific policy/batch code
   in the Node example with a usable async host binding. Validate application
   integration needs without migrating Xallet or Cymonkey in this pass.
5. **Backend compatibility:** prove a second backend, then plan WASI, HashiCorp
   and native Go integration. Those are not provided by this prototype.
6. **Feature scope:** decide where schema, instance management, packaging,
   observability and graceful draining belong. Keep the portable engine small.

The Go implementation continues to supply all existing features. The goal is
compatible host and guest implementations, not a full engine migration. The C
engine is an optional embedding path; native language implementations can also
participate through the shared contract and explicit bridges. See
[interoperability](../../docs/plugin-interoperability.md). No production code
path selects the experimental engine automatically.
