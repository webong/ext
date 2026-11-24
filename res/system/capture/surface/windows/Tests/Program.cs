using System.Buffers.Binary;
using System.IO.Compression;
using System.Runtime.InteropServices;
using System.Text.Json;
using Webong.Ext.System.Windows;

var native = new FakeWindow();
var surface = new WindowSurface(native);
var target = surface.ObserveWindow(42);
using var fixtures = JsonDocument.Parse(File.ReadAllText(Path.Combine(AppContext.BaseDirectory, "cases.json")));
var contract = fixtures.RootElement.GetProperty("windowsControl");
Check(target.Id == contract.GetProperty("windowId").GetString() && target.ProcessStartId == "1001", "Go window identity");
Check(Marshal.SizeOf<NativeInput>() == (IntPtr.Size == 8 ? 40 : 28), "Win32 INPUT ABI");
Check(surface.ObserveWindows(1) is { Truncated: true, Windows.Count: 1 }, "bounded observation");
Throws<ArgumentOutOfRangeException>(() => surface.ObserveWindows(0));
Throws<ArgumentOutOfRangeException>(() => surface.Move(target, new(10, 0)));
Check(native.Events.Count == 0, "out-of-bounds input sent nothing");

native.Window = native.Window with { ProcessStart = 1002 };
ThrowsCode(() => surface.PressKey(target, "enter"), SurfaceError.Stale);
native.Window = target.Native with { Bounds = target.Bounds with { X = 10 } };
await ThrowsCodeAsync(() => surface.CaptureAsync(target), SurfaceError.Stale);
native.Window = target.Native;
native.Focus = 99;
ThrowsCode(() => surface.TypeText(target, "text"), SurfaceError.NotFocused);
native.Focus = 42;
native.Hit = false;
ThrowsCode(() => surface.Click(target, new(1, 2)), SurfaceError.NotFocused);
Check(native.Events.Count == 0, "wrong focus and wrong hit sent nothing");
native.Hit = true;
surface.Move(target, new(1, 2));
Check(native.Cursor == new SurfacePoint(-19, 6), "window-local point mapped to physical pixels");

await ThrowsCodeAsync(() => surface.CaptureAsync(target, new(MaxPixels: 99)), SurfaceError.LimitExceeded);
Check(native.CaptureCount == 0, "pixel limit checked before native allocation");
await ThrowsCodeAsync(() => surface.CaptureAsync(target, new(MaxBytes: 42)), SurfaceError.LimitExceeded);
native.DenyCapture = true;
await ThrowsCodeAsync(() => surface.CaptureAsync(target), SurfaceError.Permission);
native.DenyCapture = false;
var capture = await surface.CaptureAsync(target);
Check(capture.Width == 10 && capture.Height == 10 && capture.Format == "image/png", "capture metadata");
Check(ReadFirstPixel(capture.Data).SequenceEqual(new byte[] { 35, 20, 10 }), "PNG uses RGB channel order");

var blocked = new ManualResetEventSlim();
var resume = new ManualResetEventSlim();
using (var cancellation = new CancellationTokenSource())
{
    native.OnCapture = () => { blocked.Set(); resume.Wait(TimeSpan.FromSeconds(5)); };
    try
    {
        var pending = surface.CaptureAsync(target, cancellationToken: cancellation.Token);
        Check(blocked.Wait(TimeSpan.FromSeconds(5)), "capture entered native call");
        cancellation.Cancel();
        try { await pending; throw new Exception("expected capture cancellation"); }
        catch (OperationCanceledException) { }
        await ThrowsCodeAsync(() => surface.CaptureAsync(target), SurfaceError.Unavailable);
    }
    finally { resume.Set(); native.OnCapture = null; }
}
native.NextAccepted = 2;
ThrowsCode(() => surface.PressKey(target, "enter", ["shift", "alt"]), SurfaceError.Unavailable);
Check(native.Events.Count == 4 && native.Events[2].IsRelease && native.Events[2].KeyCode == 0x12
    && native.Events[3].IsRelease && native.Events[3].KeyCode == 0x10, "partially accepted keys released in reverse");
native.Events.Clear();
Throws<ArgumentException>(() => surface.PressKey(target, "enter", ["shift", "SHIFT"]));
Throws<ArgumentException>(() => surface.TypeText(target, "\ud800"));
ThrowsCode(() => surface.TypeText(target, "ab", new(MaxTextRunes: 1)), SurfaceError.LimitExceeded);
surface.TypeText(target, "🧪", new(MaxTextRunes: 1));
Check(native.Events.Count == 4, "Unicode surrogate pair is one scalar and a balanced native batch");
native.Events.Clear();
native.AfterSend = () => native.Focus = 99;
ThrowsCode(() => surface.Drag(target, new(0, 0), new(1, 1)), SurfaceError.NotFocused);
Check(native.Events is [_, { IsRelease: true }], "focus loss mid-drag released its held button");
native.AfterSend = null;
native.Focus = 42;

using (var lifetime = new CancellationTokenSource())
{
    native.Events.Clear();
    using var session = surface.OpenPointerSession(target, cancellationToken: lifetime.Token);
    session.Down(new(2, 2));
    session.Down(new(2, 2), PointerButton.Right);
    ThrowsCode(() => surface.Click(target, new(1, 2)), SurfaceError.InputBusy);
    var last = native.Cursor;
    native.Focus = 99;
    lifetime.Cancel();
    await session.Completion.WaitAsync(TimeSpan.FromSeconds(5));
    Check(native.Events is [_, _, { IsRelease: true }, { IsRelease: true }] && native.Cursor == last,
        "cancellation released only held buttons without moving the cursor");
    Throws<ObjectDisposedException>(() => session.Move(new(1, 2)));
}
native.Focus = 42;
native.Events.Clear();
var disposed = surface.OpenPointerSession(target);
disposed.Down(new(2, 2));
disposed.Dispose();
disposed.Dispose();
Check(disposed.Completion.IsCompletedSuccessfully && native.Events is [_, { IsRelease: true }], "idempotent disposal");

native.Events.Clear();
using (var watched = surface.OpenPointerSession(target))
{
    watched.Down(new(2, 2));
    native.Focus = 99;
    await watched.Completion.WaitAsync(TimeSpan.FromSeconds(5));
}
Check(native.Events is [_, { IsRelease: true }], "focus loss while idle releases held input");
native.Focus = 42;

native.Events.Clear();
using (var timed = surface.OpenPointerSession(target, TimeSpan.FromMilliseconds(250)))
{
    timed.Down(new(2, 2));
    await timed.Completion.WaitAsync(TimeSpan.FromSeconds(5));
}
Check(native.Events is [_, { IsRelease: true }], "expiry releases held input");
Console.WriteLine("PASS window scope, Go identity, capture bounds and denial, and input cleanup");

if (OperatingSystem.IsWindows())
    await NativeFixture.Run(args.Contains("--require-interactive"));
else
    Console.WriteLine("SKIP native window fixture requires Windows");

static void Check(bool value, string detail)
{
    if (!value) throw new Exception(detail);
}

static void Throws<T>(Action work) where T : Exception
{
    try { work(); }
    catch (T) { return; }
    throw new Exception($"Expected {typeof(T).Name}");
}

static void ThrowsCode(Action work, SurfaceError code)
{
    try { work(); }
    catch (SurfaceException e) when (e.Code == code) { return; }
    throw new Exception($"Expected {code}");
}

static async Task ThrowsCodeAsync(Func<Task> work, SurfaceError code)
{
    try { await work(); }
    catch (SurfaceException e) when (e.Code == code) { return; }
    throw new Exception($"Expected {code}");
}

static byte[] ReadFirstPixel(byte[] png)
{
    using var idat = new MemoryStream();
    for (int i = 8; i < png.Length;)
    {
        int size = BinaryPrimitives.ReadInt32BigEndian(png.AsSpan(i));
        if (png.AsSpan(i + 4, 4).SequenceEqual("IDAT"u8)) idat.Write(png, i + 8, size);
        i += size + 12;
    }
    idat.Position = 0;
    using var zlib = new ZLibStream(idat, CompressionMode.Decompress);
    byte[] row = new byte[4];
    zlib.ReadExactly(row);
    return row[1..];
}

internal sealed class FakeWindow : IWindowNative
{
    internal NativeWindow Window = new(42, 2, 101, 1001, new(-20, 4, 10, 10));
    internal volatile int Focus = 42;
    internal bool Hit = true;
    internal SurfacePoint Cursor;
    internal bool DenyCapture;
    internal int CaptureCount;
    internal int? NextAccepted;
    internal Action? OnCapture, AfterSend;
    internal List<NativeInput> Events { get; } = [];

    public NativeWindow Observe(nint handle) => Window with { Handle = handle };
    public void Enumerate(Func<nint, bool> visit) { if (visit(42)) visit(43); }
    public nint Foreground() => Focus;
    public bool KeyboardTargets(nint window) => Focus == window;
    public bool PointerTargets(nint window, SurfacePoint screen) => Hit;
    public void SetCursor(SurfacePoint screen) => Cursor = screen;
    public bool CursorAt(SurfacePoint screen) => Cursor == screen;
    public bool IsDown(ushort virtualKey) => false;
    public byte[] Capture(NativeWindow window, int maxBytes, CancellationToken cancellationToken)
    {
        CaptureCount++;
        OnCapture?.Invoke();
        cancellationToken.ThrowIfCancellationRequested();
        if (DenyCapture) throw WindowSurface.Failure(SurfaceError.Permission, "Protected fixture.");
        return SurfacePng.Encode(window.Bounds.Width, window.Bounds.Height, maxBytes, (_, row) =>
        {
            row[0] = 10; row[1] = 20; row[2] = 35;
        }, cancellationToken);
    }

    public int Send(NativeInput[] events)
    {
        int count = NextAccepted ?? events.Length;
        NextAccepted = null;
        Events.AddRange(events.Take(count));
        AfterSend?.Invoke();
        return count;
    }
}
