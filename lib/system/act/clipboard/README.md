# Clipboard libraries

`lib/system/act/clipboard` owns the direct native clipboard implementations:
`ios` supplies the Swift `ExtClipboard` library, `android` the Java library,
and `windows` the C# `Ext.Clipboard.Windows` library. Each remains usable by a
native application without hosting an Ext guest. Their shared policy, bounds,
generation and error-code behavior is checked against
`res/system/testdata/clipboard/v1` and `res/system/testdata/codes.json`.

The Go clipboard contract remains at the published
`res/system/capture/clipboard` import path. CTX reaches this Windows
implementation through the installed Ext resource guest in
`res/system/capture/clipboard/windows/Guest`. No Go clipboard mechanism is
copied into `lib/system`.
