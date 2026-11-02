# Apple clipboard binding

`ExtClipboard` supplies portable clipboard values and a `@MainActor`
`SystemClipboard` implementation over `NSPasteboard` on macOS and `UIPasteboard`
on iOS. The caller fixes allowed formats and byte bounds before access.

Native clipboard APIs do not provide an atomic cross-process compare-and-swap.
The implementation checks its opaque generation immediately before mutation
and returns a dispatch receipt; callers that need verification perform another read.
`currentGeneration()` does not read content and remains available under a write-only policy.
