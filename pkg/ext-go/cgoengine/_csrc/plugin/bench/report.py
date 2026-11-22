"""Aggregate independent runs; retain raw measurements for audit/reproduction."""
import collections
import json
import statistics
import sys
rows = [json.loads(line) for line in open(sys.argv[1])]
groups = collections.defaultdict(list)
for row in rows:
    groups[row['engine'], row['payload_bytes'], row['concurrency']].append(row)
print('# C engine prototype measurements\n')
print(f"Host: {rows[0]['os']}/{rows[0]['arch']}, {rows[0]['go_version']}. Medians of three fresh-process runs.\n")
print('| Engine | Payload bytes | Concurrency | Startup ms | p50 µs | p95 µs | Calls/s | Peak host RSS MiB | Go allocations KiB/call |')
print('| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |')
for (engine, size, concurrency), values in sorted(groups.items(), key=lambda p:(p[0][1],p[0][2],p[0][0])):
    def median(key): return statistics.median(v[key] for v in values)
    print(f"| {engine} | {size} | {concurrency} | {median('startup_us')/1000:.2f} | {median('p50_us'):.1f} | {median('p95_us'):.1f} | {median('calls_per_second'):.0f} | {median('host_peak_rss_bytes')/2**20:.1f} | {median('go_allocated_bytes_per_call')/1024:.1f} |")
print('''
Both paths run the same Go JSON-line echo guest with empty environment and ten
warmup calls. Calls include host request encoding and response decoding. The
C path uses the shared engine through cgo; the Go path uses plugin.Session and
the existing JSON-line backend. These are end-to-end implementation comparisons,
not isolated FFI costs or evidence that one language is intrinsically faster.

RSS is peak host-process memory (including the C engine); guest memory
is excluded. Go allocation metrics exclude C allocations. Large-message RSS is
not steady-state retained memory. Startup includes selection, launch and handshake.
Concurrent calls share one serialized session. No WASM, native plugin, streaming,
foreign-language overhead or long-duration workload conclusions follow from this run.
''')
