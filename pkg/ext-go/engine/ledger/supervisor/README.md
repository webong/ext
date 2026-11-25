# Supervisor compatibility records

Only aliases of `core/ledger/supervisor` records remain here. The Go process
supervisor has been retired. Use `cengine/ledger/supervisor.OpenNative` with an
explicitly opened C ledger store. The C supervisor owns process lifecycle,
restarts, bounded logs, leases and orphan handling; consumers supply policy.
