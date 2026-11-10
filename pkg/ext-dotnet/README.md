# ext for .NET

[`ExtIO.Host`](ExtIO.Host/README.md) is the ext plugin host for .NET: it runs plugins through the
C engine (`lib/plugin`) with P/Invoke, loading `libext_host` at run time. The Windows-native .NET
libraries of ext (`Ext.Audio.Windows`, `Ext.Clipboard.Windows`, `Ext.Web.Windows`, `Ext.ML.Windows`)
live beside the Go packages they bind, under `res/`.
