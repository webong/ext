# Ext.Audio.Windows

`Ext.Audio.Windows` is the Windows-native binding of the `capture/audio` contract in
`res/system`: bounded WASAPI endpoint discovery (`SystemAudio`), system or microphone
capture (`SystemAudioCapture`), and PCM playback (`SystemAudioPlayback`).

`SystemAudio.Status()` is the capability report (`res/system/testdata/capability/v1`): `complete`
when the audio service answers (with no endpoint, `Endpoints()` is an empty list), `unsupported`
with `unsupported` when there is no audio service, and `unavailable` when it failed to answer. Every
error carries a `Code` from `res/system/testdata/codes.json`, the codes Swift's
`SystemAudioPlaybackError.code` uses:

- `AudioArgumentException` (an `ArgumentException`) is `invalid`: an empty endpoint id, or an endpoint
  of the other direction. `AudioPacketException` is one too, for a packet that breaks the playback
  rule of `audio/v1`: `invalid` or `too_large`.
- `AudioException` is `busy` (the playback buffer is full, or a capture is started twice), `canceled`
  (a write or start after `Dispose`, or a canceled token), `timeout` (a listing ran past its timeout),
  `unavailable` (the endpoint is unknown or not active), `unsupported` (no audio service) or `io`
  (another WASAPI failure; NAudio's `COMException` is the inner exception).

Every call is bounded and cancellable (rule 4 of `docs/adr-architecture.md`):

- `Endpoints(timeout, cancellationToken)` stops at its timeout (five seconds by default, as Go; above
  zero and at most an hour) or its token, as `timeout` or `canceled`. The bound is judged between
  WASAPI calls.
- A capture runs under `AudioCaptureLimits`: a maximum duration (above zero, at most an hour) and a
  byte limit (at most 1 GiB), five minutes and 64 MiB by default. It stops by itself at either, and
  when the token given to `Start` is canceled; the last buffer is cut to the whole frames that fit.
  `Stopped` reports an `AudioCaptureStop` whose `Reason` is Go's `media` stop reason: `duration`,
  `byte_limit`, `canceled`, `closed` (it was disposed) or `capture_failed` (with NAudio's exception
  as `Error`), and the bytes it delivered.
- Playback `Write` never blocks: a full buffer is `busy`.

The library does not choose endpoints, request approval, encode or transmit samples, retain
credentials, or expose a remote-control protocol. A consumer such as Cymonkey supplies
those layers. The types stay in the namespace `Webong.Ext.System.Windows`, as they were
when the audio and clipboard code shared one assembly.

```powershell
dotnet build .\Ext.Audio.Windows.csproj
```

## Install

`Ext.Audio.Windows` is released as a NuGet package of that name, at the version of the `ext-windows-vX.Y.Z` tag
(see [module versioning](https://github.com/webong/ext/blob/main/docs/module-versioning.md)). The types are in the namespace `Webong.Ext.System.Windows`.

```powershell
dotnet add package Ext.Audio.Windows
```

The package is attached to the GitHub release of its tag with a checksum file and a provenance
attestation, and is pushed to a NuGet feed when the repository is configured with one.
