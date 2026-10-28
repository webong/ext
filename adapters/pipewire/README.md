# PipeWire audio adapter

`github.com/webong/ext/adapters/pipewire` implements `res/system/audio` with
`pw-dump`, `pw-record`, and `pw-play`. It owns PipeWire PCM device listing,
direction-prefixed device identities, and bounded S16_LE streaming.

The caller selects the device and supplies the packet bounds; the adapter
chooses no card, default device, mixer state, or routing policy. PipeWire is a
selectable native audio stack, so this backend is an adapter. The shared
`res/system/audio` contract and the PulseAudio adapter remain separate, and no
library selects among them by name.
