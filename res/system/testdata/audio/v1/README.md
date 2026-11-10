# audio v1

`cases.json` covers the rules of the audio contract. The playback sinks (Swift, Java, C#) share one rule;
the device snapshot, the limits and the packet format of the engine's backends only exist in Go today, so
those sections are Go's and are written for the bindings that follow.

- **Packets** (Go `ValidatePacket`): a PCM16 packet has data, a rate from 8 to 192 kHz, 1 to 8 channels and
  a size that is whole frames (`channels` times 2 bytes); otherwise it is `invalid`. A packet over the
  caller's ceiling is `too_large`. The format is judged before the size.
- **Playback**: the same rule for the packet a stereo, 48 kHz, 16-bit sink takes, whose frame is 4 bytes
  and whose default ceiling is 3,840 bytes (20 ms): Go through `ValidatePacket`, Swift
  `SystemAudioPlayback.validate`, Java `PlaybackPacket.validate` and C# `SystemAudioPlayback.CheckPacket` (`AudioPacketException.Code`).
  An empty packet or a partial frame is `invalid` and a packet over the ceiling is `too_large`, frames
  first. A full playback buffer is `busy` (Swift `bufferFull`, C# `IOException`), a closed sink `canceled`,
  and a native failure `io`; none of those can be run without a device.
- **Limits** (Go): `maxDevices` is 1 to 1024 and `maxPacketBytes` 1 to 1 MiB (defaults 64 and 192,000),
  else `invalid`; zero is Go's default, so it is not a case.
- **Devices** (Go `ValidateSnapshot`): a snapshot has a backend of at most 64 bytes, a timestamp (RFC 3339)
  and a status of `complete`, `partial` or `unsupported`; an `unsupported` status says why with the reason
  `unsupported` or `tool_missing` (the adapter that owns a missing tool reports `tool_missing`), and the
  other two take none. A device has an id and a name of 1 to 256 bytes without control characters or edge
  spaces, a unique id, a kind of `output`, `input` or `monitor`, a state and a format of at most 64
  bytes, a rate (0 for unknown) from 8 to 192 kHz and 0 (unknown) or 1 to 8 channels. More devices than the
  limit is `too_large`.
- **Select** (Go `Select`): the first device of a kind, or the named one; a name that is unknown or of
  another kind is `unavailable`, never a fallback.

Not covered: capture and playback themselves, endpoint enumeration (WASAPI, `AudioTrack`, `AVAudioEngine`)
and the backpressure of a real buffer.
