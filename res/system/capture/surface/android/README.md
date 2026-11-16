# Android surface library

The portable Java records match the Go and Swift surface, accessibility, and
permission models. The Android binding reports displays visible through
`DisplayManager` and, when an `Activity` is supplied, that application's visible
window and view tree. Android applications cannot enumerate other applications'
windows or views through public APIs.

Both observers are bounded in time and cancellable, as Go's `DiscoverSurfaces` and
`DiscoverAccessibility`: `SurfaceOptions` and `AccessibilityOptions` carry a timeout (five seconds by
default), and `observe(activity, cancellation)` takes a `SurfaceCancellation`, which another thread can
`cancel()` while the main thread observes and which can carry its own deadline (`withTimeout`). The
observer checks before each display or view and at the end, and throws
`SurfaceCancellationException` (a `CancellationException`) with the code `timeout` or `canceled`.
The permission observer reads constants and waits on nothing, so it takes neither.
`SystemMediaProjection.start` takes a maximum duration of 1 ms to one hour, Go's capture `MaxDuration`
range; when it passes, the projection stops as `close()` does and the stop callback runs. Closing
earlier is the cancel.

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
