# Wayland desktop adapter

`github.com/webong/ext/adapters/wayland` implements `res/system.DesktopDriver`
with `grim` and `wtype`. It owns compositor command construction, PNG dimension
checks, and keyboard mapping.

Wayland denies arbitrary clients window-scoped state, and `wtype` provides no
pointer input, so pointer operations fail explicitly. Recording is an explicit
gap: it requires a compositor-specific recorder or an XDG desktop portal, and
this adapter does not select one for the caller.

The caller selects this driver and every bound. The adapter chooses no product
identity, output, or approval flow.
