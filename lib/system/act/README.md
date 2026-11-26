# Native actions

`lib/system/act` contains reusable implementations of actions exposed by OS
APIs. `clipboard` reads and changes the system clipboard; `share` handles native
share and keyboard extension operations; `capture` handles audio and surface
capture, playback and input. Each platform library can be used by
an application directly or by an Ext resource guest in `res/system`.

The caller chooses the target and authorization policy. These libraries do not
depend on an Ext guest or product.
