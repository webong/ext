# Android surface library

The portable Java records match the Go and Swift surface, accessibility, and
permission models. The Android binding reports displays visible through
`DisplayManager` and, when an `Activity` is supplied, that application's visible
window and view tree. Android applications cannot enumerate other applications'
windows or views through public APIs.

Accessibility labels and values are opt-in. `SystemMediaProjection` wraps a
fresh, caller-consented `MediaProjection` for display surfaces and optional
system-audio or microphone capture. The application remains responsible for the
capture consent flow, foreground-service declarations, audio focus, encoding and
lifecycle policy.

The library does not expose raw native handles, open settings, synthesize
cross-application input, or provide remote access. Additional Android shell
mechanisms belong here when implemented through public Android APIs; consumer
grants and remote control do not.

The Java package stays `io.github.webong.ext.system`; the other Android bindings
of `res/system` live beside their Go packages: [`capture/share`](../../share/android/README.md),
[`capture/audio`](../../audio/android/README.md) and [`service/host`](../../../service/host/android/README.md).
