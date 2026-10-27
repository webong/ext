# ALSA audio adapter

`github.com/webong/ext/adapters/alsa` implements `res/system/audio` with
`aplay -L`, `arecord -L`, `aplay`, and `arecord`. It owns ALSA PCM listing,
direction-prefixed device identities, and bounded S16_LE streaming.

The caller selects the device and supplies the packet bounds; the adapter
chooses no card, default device, mixer state, or routing policy. IDs are
`output:<pcm>` and `input:<pcm>` because ALSA lists names such as `default` in
both directions. `null` PCMs are omitted, and unknown sample formats are
reported as absent rather than guessed.

ALSA is a selectable native audio stack, so this backend is an adapter. The
shared `res/system/audio` contract and the PulseAudio adapter remain separate,
and no library selects among them by name.
