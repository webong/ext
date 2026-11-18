# ext for .NET

[`ExtIO.Host`](ExtIO.Host/README.md) is the ext plugin host for .NET: it runs plugins through the
C engine (`lib/plugin`) with P/Invoke, loading `libext_host` at run time. The Windows-native .NET
libraries of ext (`Ext.Audio.Windows`, `Ext.Clipboard.Windows`, `Ext.Web.Windows`, `Ext.ML.Windows`)
live beside the Go packages they bind, under `res/`.

## Release

`ExtIO.Host` is released as the NuGet package of that name, at the version of the `ext-dotnet-vX.Y.Z` tag: the
.NET language package, built and tested on Linux. The Windows libraries of `res/` are the separate
`ext-windows` package. There is no .NET guest SDK yet. See [module versioning](../../docs/module-versioning.md).
