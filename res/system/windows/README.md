# Ext.System.Windows

`Ext.System.Windows` contains reusable Windows-native shell primitives owned by
`res/system`. The first surface is bounded WASAPI endpoint discovery, system or
microphone capture, and PCM playback.

The library does not choose endpoints, request approval, encode or transmit
samples, retain credentials, or expose a remote-control protocol. A consumer
such as Cymonkey supplies those layers.

```powershell
dotnet build .\Ext.System.Windows.csproj
```
