# Android system observation

The portable Java records match the Go and Swift surface model. The Android
binding reports displays visible through `DisplayManager` and, when an
`Activity` is supplied, that application's visible window. Android applications
cannot enumerate other applications' windows through public APIs.

The observer reads geometry only. It does not capture pixels, expose native
window identifiers, request permission, or synthesize input.
