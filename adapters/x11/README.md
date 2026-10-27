# X11 desktop adapter

`github.com/webong/ext/adapters/x11` implements `res/system.DesktopDriver` with
`import`, `xdotool`, and `ffmpeg`. It owns command construction, key and pointer
mapping, PNG dimension checks, and the bounded recording process.

The caller selects this driver, the desktop session, the capture/input bounds,
and the recording format and size. The adapter chooses no product identity,
display, destination, or approval flow. It performs whole-desktop operations
because command-line X11 tools cannot address the opaque window identities
`res/system` observes.

Recording needs `DISPLAY` and `ffmpeg`; capture and input need `import` and
`xdotool`. Missing tools fail explicitly instead of falling back to another
mechanism.
