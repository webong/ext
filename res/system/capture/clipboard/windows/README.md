# Ext.Clipboard.Windows

`Ext.Clipboard.Windows` is the Windows-native binding of the `capture/clipboard` contract in
`res/system`: generation-checked Win32 clipboard access (`SystemClipboard`). It accepts an
explicit format, item, and byte policy (`ClipboardPolicy`), keeps one clipboard item per write
(as Windows does), and converts UTF-16 text. It has no package dependencies.

`SystemClipboard.Status()` is the capability report (`res/system/testdata/capability/v1`): `complete`
when this session has a clipboard, `unavailable` with `no_device` on a window station with no
interactive desktop (a service session), and `unsupported` off Windows. Every error is a
`ClipboardException` whose `Code` is in `res/system/testdata/codes.json`: `invalid`, `denied`
(the policy refuses it, or the clipboard holds none of the allowed formats), `too_large`, `changed`,
`busy` (another process held the clipboard while it was retried), `unavailable` (there is no
clipboard to open) and `io` (a Win32 call failed; its `Win32Exception` is the inner exception).
No message names a format or other value it was given. The tests run the shared `clipboard/v1` and
`capability/v1` fixtures, and each Win32 failure through a fake clipboard, before the round trips.

The library does not choose clipboard formats, request approval, retain credentials, or expose
a remote-control protocol. A consumer such as Cymonkey supplies those layers. The types stay in
the namespace `Webong.Ext.System.Windows`, as they were when the audio and clipboard code
shared one assembly.

```powershell
dotnet build .\Ext.Clipboard.Windows.csproj
```

## Install

`Ext.Clipboard.Windows` is released as a NuGet package of that name, at the version of the `ext-dotnet-vX.Y.Z` tag
(see [module versioning](https://github.com/webong/ext/blob/main/docs/module-versioning.md)). The types are in the namespace `Webong.Ext.System.Windows`.

```powershell
dotnet add package Ext.Clipboard.Windows
```

The package is attached to the GitHub release of its tag with a checksum file and a provenance
attestation, and is pushed to a NuGet feed when the repository is configured with one.
