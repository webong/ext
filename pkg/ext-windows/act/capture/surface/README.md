# Ext.System.Windows

`Ext.System.Windows` gives .NET 8 applications on Windows 10 1703 or later a local API for observing an explicitly selected window, capturing a bounded PNG, and sending window-relative pointer and keyboard input. The app chooses the window, decides which actions to allow, and owns authentication and transport.

```csharp
using Webong.Ext.System.Windows;

var desktop = new WindowSurface();
var target = desktop.ObserveWindow(selectedHwnd);
var frame = await desktop.CaptureAsync(target, new(MaxPixels: 2_000_000),
    cancellationToken);
desktop.Click(target, new SurfacePoint(50, 40), cancellationToken: cancellationToken);
```

`ObserveWindows()` provides a bounded list of visible, identifiable top-level windows for a caller's picker. A reference binds the HWND, native window thread, process ID, process creation time, and observed bounds. Its public ID and process generation use the Go surface contract. A stale reference is rejected after the window closes, its identity changes, or its bounds change. Observe the selected window again after a move or resize.

- Bounds use physical pixels in the virtual desktop. Pointer coordinates are relative to the selected bounds. Input checks foreground focus and pointer hit-testing. The calling app handles foreground activation. Windows system input is global and focus may change between the last check and dispatch.
- Capture requests a bitmap of the selected window from Windows and returns PNG bytes. It refuses reported display affinity restrictions. Some windows cannot render to this capture mechanism. Capture uses the selected window buffer throughout. Defaults are 16,777,216 pixels, 16 MiB encoded, and a five-second wait. If the native renderer blocks past the deadline, its work retains the process's one capture slot until it returns.
- Pointer sessions support holds across calls. Scope loss observed during an operation or the periodic focus check, lifetime or operation cancellation, expiry, and disposal attempt to release every button the session pressed. Cleanup does not move the pointer. Await `Completion` to detect failure after asynchronous cleanup. By default a session expires after five minutes.
- `SurfaceException.Code` identifies stale references, unavailable native operations, permission denial, focus loss, size limits, and input already in use. Invalid arguments and cancellation use the usual .NET exception types.

## Install and verify

Download `Ext.System.Windows.<version>.nupkg` and `checksums.txt` from the corresponding `ext-windows-vX.Y.Z` GitHub release. Compare the package's SHA-256 hash with the entry in `checksums.txt`, then install from the download directory as a local NuGet source.

```powershell
Get-FileHash -Algorithm SHA256 .\Ext.System.Windows.<version>.nupkg
dotnet add package Ext.System.Windows --version <version> --source <download-directory>
```

From the repository root, run the fixture on an interactive Windows desktop. The fixture sends input only to its own temporary windows. Use a test session while running it.

```powershell
dotnet run --project pkg/ext-windows/act/capture/surface/Tests/Ext.System.Windows.Tests.csproj -- --require-interactive
```
