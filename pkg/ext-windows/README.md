# ext-windows

The Windows platform package: .NET libraries for the Windows mechanisms ext uses,
the counterpart of [`ext-darwin`](../ext-darwin/README.md) and
[`ext-android`](../ext-android/README.md). Each library is usable by a Windows
application directly and by the Ext resource guests in `res/system`, which build
on it.

| Directory | Package | Provides |
| --- | --- | --- |
| [`act/capture/audio`](act/capture/audio/README.md) | `Ext.Audio.Windows` | WASAPI audio device observation and bounded PCM capture and playback |
| [`act/capture/surface`](act/capture/surface/README.md) | `Ext.System.Windows` | Selected-window capture and scoped input |
| [`act/clipboard`](act/clipboard/README.md) | `Ext.Clipboard.Windows` | The system clipboard, with bounded reads and change generations |
| [`int/model`](int/model/README.md) | `Ext.ML.Windows` | The ONNX Runtime model backend |
| [`net`](net/README.md) | `Ext.Network.Windows` | Local interface, address and resolver observation |
| [`web`](web/README.md) | `Ext.Web.Windows` | WebView2 document scripts and profile extensions for an application-owned WebView2 |

The packages release together as `ext-windows-vX.Y.Z` (see
[module versioning](../../docs/module-versioning.md)). Their behaviour is held to
the shared fixtures in `res/system/testdata` and `res/web/testdata`, which the
Swift and Java implementations in the other platform packages run too.

None of these libraries imports an Ext guest or a product, and none chooses a
policy: the caller decides what to capture, which window to act on and what to
allow. Provider- and product-specific behaviour belongs in adapters, outside ext.

```sh
dotnet run --project pkg/ext-windows/act/clipboard/Tests/Ext.Clipboard.Windows.Tests.csproj
```

Each library's `Tests` project runs the same way, on Windows.
