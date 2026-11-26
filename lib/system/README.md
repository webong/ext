# Native system libraries

`lib/system` owns reusable code that talks to implementations provided by the
operating system. The boundary is the native platform API, whether the feature
is kernel, shell, web, intelligence, action or networking. Platform
implementations live under capability areas: `web` for web, `int` for
intelligence, `act` for clipboard, share, capture and input, and `net` for
networking. Private-volume lifecycle and host-resource collection belong to
`lib/ledger`. Swift and Android Java bindings to that C collector live in
`pkg/ext-darwin` and `pkg/ext-android`.
These implementations do not import CTX, CTN, a product adapter or an Ext guest.

The Ext-facing contracts and guests belong in `res/`. A guest may use its
language's SDK from `pkg/` and call one of these libraries; the library does
not depend on the guest. `pkg/ext-darwin` and `pkg/ext-android` package the
native library sources for direct use by applications.

`res/system`, `res/web/host` and `res/int` retain their published Go APIs while
those callers are migrated. Do not copy their Go implementations into this
directory. Shared behavior is checked against the existing versioned fixtures
in `res/*/testdata` during the transition.
