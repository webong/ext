# Wayland desktop adapter

`github.com/webong/ext/adapters/wayland` implements `res/system.DesktopDriver`
with `grim` and `wtype`. It owns compositor command construction, PNG dimension
checks, and keyboard mapping.

Wayland denies arbitrary clients window-scoped state, and `wtype` provides no
pointer input. A caller may explicitly configure `Pointer: "ydotool"` to route
move, click, and drag through `ydotool`; that requires a running `ydotoold`
daemon with `/dev/uinput` access, and the caller owns that privilege. Without
it, pointer operations fail explicitly. `ydotool` has no wheel operation, so
scroll is unavailable.

A caller may explicitly configure `Recorder: "wf-recorder"` for bounded
desktop-scoped recording. The adapter maps `webm` to `libvpx-vp9`/`webm` and
`mp4` to `libx264`/`mp4`, uses `--framerate`, `--muxer`, and `--file pipe:1`,
and enforces `MaxDuration`/`MaxBytes` while reading. Stop sends SIGINT, then
forcibly kills after a bounded grace period. Window-scoped recording is not
supported; no desktop portal or approval flow is used.

The caller selects this driver and every bound. The adapter chooses no product
identity, output, or approval flow.
