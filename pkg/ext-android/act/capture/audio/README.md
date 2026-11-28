# Android audio library

`SystemAudioPlayback` provides bounded, nonblocking PCM playback for the
`capture/audio` contract. The application remains responsible for audio focus,
encoding and lifecycle policy. Capture of system audio or the microphone goes through
`SystemMediaProjection` in [`capture/surface`](../../surface/android/README.md).

Errors carry the codes of `testdata/codes.json`, as C# and Swift do. A refused packet is a
`PlaybackException` (an `IllegalArgumentException`): `invalid`, or `too_large` with the ceiling in
its message. A sink that cannot take the packet throws `AudioException` (an `IllegalStateException`):
`canceled` once it is closed (before the packet is judged), `busy` when the buffer accepts nothing,
`io` for a failed native write, and `unavailable` when the player cannot be opened or has gone. A
partial write is backpressure and returns the bytes accepted. The write rule is the portable
`PlaybackSink` over a `PlaybackTrack`, so it is tested without a device (`PlaybackSinkTests`).
