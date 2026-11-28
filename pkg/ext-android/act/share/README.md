# Android share and keyboard library

`io.github.webong.ext.system.share` normalizes `ACTION_SEND` and
`ACTION_SEND_MULTIPLE` intents into the same bounded model as the Go and Swift
contracts, copying attachments into a directory the application supplies.
`io.github.webong.ext.system.keyboard` reads selection and context, inserts and
deletes bounded text, submits the editor action, and switches keyboards.
`SystemShare` and `SystemKeyboard` adapt the Android `Intent` and
`InputMethodService` APIs. Neither provides an input view, capability inbox,
event schema, extension registry, launch policy, or application identity.

`SystemShare.capture(intent, directory, limits, cancellation)` copies attachments until a
`ShareCancellation` stops it: another thread can `cancel()` it, and `withTimeout` gives it a deadline.
Go's share package copies nothing, so it has no timeout to match; `capture` without one has no deadline.
It checks before each attachment and each 64 KiB chunk, deletes the partial file of a stopped copy, and
throws `ShareException` with the kind `timeout` or `canceled`. A provider whose read blocks is seen to
stop only when the read returns. `SystemKeyboard` makes single, nonblocking editor calls and takes no
cancellation.
