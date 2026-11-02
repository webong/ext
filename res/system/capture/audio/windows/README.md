# Ext.Audio.Windows

`Ext.Audio.Windows` is the Windows-native binding of the `capture/audio` contract in
`res/system`: bounded WASAPI endpoint discovery (`SystemAudio`), system or microphone
capture (`SystemAudioCapture`), and PCM playback (`SystemAudioPlayback`).

The library does not choose endpoints, request approval, encode or transmit samples, retain
credentials, or expose a remote-control protocol. A consumer such as Cymonkey supplies
those layers. The types stay in the namespace `Webong.Ext.System.Windows`, as they were
when the audio and clipboard code shared one assembly.

```powershell
dotnet build .\Ext.Audio.Windows.csproj
```
