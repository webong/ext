# Ledger compatibility records

This package preserves aliases of `core/ledger` records for existing imports.
The Go memory store, recorder, SQLite store and supervisor implementations have
been removed. New code should import the shared records from `core/ledger` and
bind `lib/ledger` through `cengine/ledger`.

Open a native memory store with `Library.OpenStore(ctx, "memory", "", options)`;
use `cengine/ledger/supervisor.OpenNative` for process supervision. The consumer
example lives in `cengine/ledger/examples/consumer-shaped` and requires
`EXT_LEDGER_LIBRARY` pointing at the built C library.

The nested `recorder` module retains a thin native entry point. These changes
require callers of the former Go lifecycle APIs to migrate; they are not
signature-compatible replacements. The C fixtures define behavior.
