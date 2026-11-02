# ffmpeg desktop recording adapter

`github.com/webong/ext/adapters/ffmpeg` implements `media.Recorder` for the desktop of macOS and
Windows with the `ffmpeg` command-line tool. It owns `ffmpeg` invocation and the bounded recording
process. The caller selects the capture source, the format, the frame rate and the duration and
byte bounds; the adapter chooses no display, device, output path or approval flow.

Capture and input on those platforms need no tool: `surface.NativeDesktop` in `res/system` does
them with the operating system's own APIs. Recording does need an encoder, so `NativeDesktop` takes
this recorder as `NativeDesktopOptions.Recorder`:

```go
recorder, err := ffmpeg.New(ffmpeg.Config{Source: "1:none"}) // macOS: an AVFoundation device
desktop, err := surface.NewNativeDesktop(surface.NativeDesktopOptions{Recorder: recorder})
recording, err := surface.RecordDesktop(ctx, desktop, media.RecordingOptions{Format: "mp4"})
```

| Platform | ffmpeg input | `Config.Source` |
| --- | --- | --- |
| macOS | `avfoundation` | Required: `video:audio`, for example `1:none`. List devices with `ffmpeg -f avfoundation -list_devices true -i ""` |
| Windows | `gdigrab` | Optional, `desktop` (the whole virtual screen) by default |

The output is a fragmented MP4 (`libx264`) or a streaming WebM (`libvpx-vp9`) written to the stream
returned by `Record`, never to a file. `ffmpeg` ends by itself at the duration (`-t`) and size (`-fs`)
bounds, and `Stop` ends it early; fragmented output stays playable up to the last complete fragment.
A missing tool fails explicitly instead of falling back to another mechanism. Linux has its own
adapters (`x11`, `wayland`), so this one refuses it.

macOS asks the user for the Screen Recording permission the first time `ffmpeg` captures; this
adapter never requests it.

The adapter executable supports `validate`, `doctor` (checks that `ffmpeg` is on `PATH`) and
`run record <mp4|webm> <seconds>`, which writes the recording to standard output using the source
named by `EXT_FFMPEG_SOURCE`.
