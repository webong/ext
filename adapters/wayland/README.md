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
desktop-scoped recording. The adapter maps `webm` to `libvpx-vp9`/`webm`,
uses the `fps` filter, continuous frame capture, and realtime encoding.
`--file pipe:3` keeps encoded bytes separate from native stdout diagnostics.
MP4 is rejected because the pipe is not seekable. It actively enforces `MaxDuration` even without reads and
bounds reads by `MaxBytes`. Stop releases blocked reads, sends SIGINT, then
forcibly kills after a two-second grace period. It waits at most two more
seconds for exit and reports `ErrRecordingShutdown` if exit is unconfirmed.
Window-scoped recording is not
supported; no desktop portal or approval flow is used.

An injected `Starter` owns child reaping and exposes it through `Process.Done()`
(replacing `Wait()`). It must expose descriptor 3 as `Process.Stdout()`,
keeping native stdout diagnostics separate. The channel is stable and closes only after reaping;
`Signal`, `Kill`, and stdout `Close` must return promptly, and `Close` must
unblock reads. This lets shutdown wait without launching an unbounded waiter
for caller-supplied processes. The first terminal state is retained across
reads and repeated stops; shutdown errors are also reported by Stop/Close.
Cancelling a Stop context releases that caller's wait while cleanup continues.

The caller selects this driver and every bound. The adapter chooses no product
identity, output, or approval flow.

`scripts/verify-linux-desktop.sh` runs real capture and bounded WebM recording
against an isolated headless wlroots compositor, alongside PipeWire streaming.
It requires a non-root Linux user and creates no session on the user's desktop.
