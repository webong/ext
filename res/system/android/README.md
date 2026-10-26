# Android system observation

The portable Java records match the Go and Swift surface, accessibility, and
permission models. The Android binding reports displays visible through
`DisplayManager` and, when an `Activity` is supplied, that application's visible
window and view tree. Android applications cannot enumerate other applications'
windows or views through public APIs.

The observers read local state only. Accessibility labels and values are opt-in.
They do not capture pixels, expose native handles, request permission, open
settings, synthesize input, or provide remote access.
