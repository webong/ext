# Wayland desktop adapter

`github.com/webong/ext/adapters/wayland` implements `res/system.DesktopDriver`
with `grim` and `wtype`. It owns compositor command construction, PNG dimension
checks, and keyboard mapping.

Wayland denies arbitrary clients window-scoped state, and `wtype` provides no
pointer input. A caller may explicitly configure `Pointer: "ydotool"` to route
move, click, and drag through `ydotool`; that requires a running `ydotoold`
daemon with `/dev/uinput` access, and the caller owns that privilege. Without
it, pointer operations fail explicitly. `ydotool` has no wheel operation, so
scroll is unavailable. Recording is an explicit gap: it requires a
compositor-specific recorder or an XDG desktop portal approval flow, which
belongs to the consumer; the adapter does not select one.

The caller selects this driver and every bound. The adapter chooses no product
identity, output, or approval flow.
