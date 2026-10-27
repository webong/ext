# Ext.System.Windows

`Ext.System.Windows` contains reusable Windows-native shell primitives owned by
`res/system`. The surfaces are bounded WASAPI endpoint discovery, system or microphone
capture, PCM playback, and generation-checked Win32 clipboard access.

`SystemClipboard` accepts an explicit format, item, and byte policy, keeps one
clipboard item per write (as Windows does), and converts UTF-16 text. The
library does not choose endpoints or clipboard formats, request approval,
encode or transmit samples, retain credentials, or expose a remote-control
protocol. A consumer
such as Cymonkey supplies those layers.

```powershell
dotnet build .\Ext.System.Windows.csproj
```
