# Android audio library

`SystemAudioPlayback` provides bounded, nonblocking PCM playback for the
`capture/audio` contract. The application remains responsible for audio focus,
encoding and lifecycle policy. Capture of system audio or the microphone goes through
`SystemMediaProjection` in [`capture/surface`](../../surface/android/README.md).
