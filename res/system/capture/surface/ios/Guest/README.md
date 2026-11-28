# macOS surface resource guest

`ExtSurfaceGuest` is a private Ext package executable. It binds the native
`ExtSystem` window controller in `pkg/ext-darwin/act/capture/surface` and uses
`ExtHost` to obtain the C ledger's process birth token. Every capture and input
call rechecks the selected window's ID, process generation, geometry, and (for
input) focus. Window capture requires macOS 14 or later; input uses the current
macOS accessibility permission.

The `ext-macos` release builds and seals `system.surface.darwin.zip`. Install
and trust that package through `ext package` before CTX desktop commands use it.
