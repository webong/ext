# ext for .NET

[`ExtIO`](ExtIO/README.md) is the ext plugin guest SDK for .NET: a plugin written in C# (or any .NET
language) that speaks `ext.io.pc/v1` as JSON lines on standard input and output, with the base class
library only. [`ExtIO.Host`](ExtIO.Host/README.md) is the ext plugin host for .NET: it runs plugins through the
C engine (`lib/plugin`) with P/Invoke, loading `libext_plugin_host` at run time. The Windows-native .NET
libraries of ext (`Ext.Audio.Windows`, `Ext.Clipboard.Windows`, `Ext.Web.Windows`, `Ext.ML.Windows`)
live in [`pkg/ext-windows`](../ext-windows/README.md).

## Release

`ExtIO` and `ExtIO.Host` are released as the NuGet packages of those names, at the version of the
`ext-dotnet-vX.Y.Z` tag: the .NET language package, built and tested on Linux. The Windows libraries in `pkg/ext-windows` are the separate
`ext-windows` package. See [module versioning](../../docs/module-versioning.md).
