# C engine prototype measurements

Host: darwin/arm64, go1.27.1. Medians of three fresh-process runs.

| Engine | Payload bytes | Concurrency | Startup ms | p50 µs | p95 µs | Calls/s | Peak host RSS MiB | Go allocations KiB/call |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| c | 16 | 1 | 2.64 | 25.2 | 33.9 | 37353 | 7.9 | 1.4 |
| go | 16 | 1 | 3.08 | 31.9 | 39.2 | 29888 | 11.8 | 7.9 |
| c | 1024 | 1 | 2.63 | 48.5 | 67.5 | 19178 | 10.7 | 5.5 |
| go | 1024 | 1 | 3.13 | 70.8 | 110.7 | 13237 | 12.1 | 53.7 |
| c | 1024 | 8 | 2.63 | 296.3 | 1433.7 | 16562 | 12.2 | 5.6 |
| go | 1024 | 8 | 3.08 | 568.1 | 745.2 | 13198 | 12.3 | 53.7 |
| c | 1048576 | 1 | 2.82 | 24225.2 | 24640.5 | 41 | 49.7 | 4981.9 |
| go | 1048576 | 1 | 3.37 | 34953.5 | 36365.4 | 28 | 24.6 | 52292.9 |

Both paths run the same Go JSON-line echo guest with empty environment and ten
warmup calls. Calls include host request encoding and response decoding. The
C path uses the shared engine through cgo; the Go path uses plugin.Session and
the existing JSON-line backend. These are end-to-end implementation comparisons,
not isolated FFI costs or evidence that one language is intrinsically faster.

RSS is peak host-process memory (including the shared C library); guest memory
is excluded. Go allocation metrics exclude C allocations. Large-message RSS is
not steady-state retained memory. Startup includes selection, launch and handshake.
Concurrent calls share one serialized session. No WASM, native plugin, streaming,
foreign-language overhead or long-duration workload conclusions follow from this run.
