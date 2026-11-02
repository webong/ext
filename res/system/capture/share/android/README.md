# Android share and keyboard library

`io.github.webong.ext.system.share` normalizes `ACTION_SEND` and
`ACTION_SEND_MULTIPLE` intents into the same bounded model as the Go and Swift
contracts, copying attachments into a directory the application supplies.
`io.github.webong.ext.system.keyboard` reads selection and context, inserts and
deletes bounded text, submits the editor action, and switches keyboards.
`SystemShare` and `SystemKeyboard` adapt the Android `Intent` and
`InputMethodService` APIs. Neither provides an input view, capability inbox,
event schema, extension registry, launch policy, or application identity.
