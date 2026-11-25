# Native Go plugin host

This package binds `lib/plugin` through its described library interface. C owns
session admission, lifecycle, reverse-call routing and registry behavior. Go
supplies application policies and optional `engine.Backend` transports. It does
not fall back to the reference Go host.

Use `Default()` for the configured engine, or open a shared library with
`cengine/library.Open` and pass it to `Bind`. `Library.Open` accepts the shared
`engine.Options` model and a Go transport; `Library.OpenProcess` lets C launch
the guest. Close sessions and registries when finished. Installed packages use
`cengine/plugin/host`. `Library.Objects()` exposes the described C library
interface, including package-store and trust operations.

The default loader checks `EXT_PLUGIN_LIBRARY`, an embedded library provider,
and `ext-lib` beside the executable. A no-cgo Go build still needs the native
shared library at runtime.

Run the native host and standalone command acceptance gate from the repository:

```sh
bash scripts/package-acceptance.sh
```

The tests require a built `lib/plugin` library; the gate supplies it and fails
if required host checks are skipped.
