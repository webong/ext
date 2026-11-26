# Android clipboard binding

The portable Java source set defines the same bounded records as the Go and
Swift libraries. The Android source set binds them to `ClipboardManager` on the
UI thread and supports explicit text, HTML, URI, and Intent representations.

`clear` requires Android 9 (API 28), where `clearPrimaryClip` became available.
Android does not provide an atomic cross-process clipboard transaction, so the
opaque generation is checked immediately before dispatch.
`currentGeneration()` does not read content and remains available under a write-only policy.

`read`, `write` and `clear` take a `ClipboardCancellation`, as Go's `Clipboard` methods take a
context: another thread can `cancel()` it, and `withTimeout` gives it a deadline. A stop throws
`ClipboardException` with the code `timeout` or `canceled`. The last check is just before the native
write, and a dispatched write is not undone.
