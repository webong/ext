# Android system library

The portable Java records match the Go and Swift surface, accessibility, and
permission models. The Android binding reports displays visible through
`DisplayManager` and, when an `Activity` is supplied, that application's visible
window and view tree. Android applications cannot enumerate other applications'
windows or views through public APIs.

Accessibility labels and values are opt-in. `SystemMediaProjection` wraps a
fresh, caller-consented `MediaProjection` for display surfaces and optional
system-audio or microphone capture. `SystemAudioPlayback` provides bounded,
nonblocking PCM playback. The application remains responsible for the capture
consent flow, foreground-service declarations, audio focus, encoding and
lifecycle policy.

`io.github.webong.ext.system.share` normalizes `ACTION_SEND` and
`ACTION_SEND_MULTIPLE` intents into the same bounded model as the Go and Swift
contracts, copying attachments into a directory the application supplies.
`io.github.webong.ext.system.keyboard` reads selection and context, inserts and
deletes bounded text, submits the editor action, and switches keyboards.
`SystemShare` and `SystemKeyboard` adapt the Android `Intent` and
`InputMethodService` APIs. Neither provides an input view, capability inbox,
event schema, extension registry, launch policy, or application identity.

The library does not expose raw native handles, open settings, synthesize
cross-application input, or provide remote access. Additional Android shell
mechanisms belong here when implemented through public Android APIs; consumer
grants and remote control do not.
