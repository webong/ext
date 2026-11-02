# Ext.Clipboard.Windows

`Ext.Clipboard.Windows` is the Windows-native binding of the `capture/clipboard` contract in
`res/system`: generation-checked Win32 clipboard access (`SystemClipboard`). It accepts an
explicit format, item, and byte policy (`ClipboardPolicy`), keeps one clipboard item per write
(as Windows does), and converts UTF-16 text. It has no package dependencies.

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
