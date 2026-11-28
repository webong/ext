using Microsoft.Win32.SafeHandles;
using System.Runtime.ExceptionServices;
using System.Runtime.InteropServices;
using static Webong.Ext.System.Windows.WindowSurface;

namespace Webong.Ext.System.Windows;

internal sealed class Win32Surface : IWindowNative
{
    public NativeWindow Observe(nint handle)
    {
        using var dpi = new PhysicalCoordinates();
        if (GetAncestor(handle, 2) != handle || !IsWindowVisible(handle) || IsIconic(handle)
            || DwmGetWindowAttribute(handle, 14, out var cloaked, sizeof(int)) != 0 || cloaked != 0
            || !GetWindowRect(handle, out var rect))
            throw Failure(SurfaceError.Stale, "The selected top-level window is no longer visible.");

        var width = (long)rect.Right - rect.Left;
        var height = (long)rect.Bottom - rect.Top;
        var thread = GetWindowThreadProcessId(handle, out var pid);
        if (thread == 0 || pid == 0 || width is <= 0 or > int.MaxValue || height is <= 0 or > int.MaxValue)
            throw Failure(SurfaceError.Stale, "The selected window has no valid process or bounds.");

        using var process = OpenProcess(0x1000, false, pid); // PROCESS_QUERY_LIMITED_INFORMATION
        if (process.IsInvalid) throw Win32Failure("OpenProcess", SurfaceError.Stale);
        if (!GetProcessTimes(process, out var created, out var exited, out _, out _))
            throw Win32Failure("GetProcessTimes", SurfaceError.Stale);
        if (created.Value == 0 || exited.Value != 0 || GetWindowThreadProcessId(handle, out var currentPid) != thread
            || currentPid != pid)
            throw Failure(SurfaceError.Stale, "The selected window's process changed.");
        return new(handle, thread, pid, created.Value, new(rect.Left, rect.Top, (int)width, (int)height));
    }

    public void Enumerate(Func<nint, bool> visit)
    {
        var stopped = false;
        ExceptionDispatchInfo? error = null;
        bool complete = EnumWindows((handle, _) =>
        {
            try { return !(stopped = !visit(handle)); }
            catch (Exception ex) { error = ExceptionDispatchInfo.Capture(ex); return false; }
        }, 0);
        error?.Throw();
        if (!complete && !stopped) throw Win32Failure("EnumWindows");
    }

    public byte[] Capture(NativeWindow window, int maxBytes, CancellationToken token)
    {
        using var dpi = new PhysicalCoordinates();
        CheckCapturePermission(window.Handle);
        if (Observe(window.Handle) != window)
            throw Failure(SurfaceError.Stale, "The window changed before capture.");
        var bounds = window.Bounds;
        var dc = CreateCompatibleDC(0);
        if (dc == 0) throw Win32Failure("CreateCompatibleDC");
        try
        {
            var info = new BitmapInfo
            {
                Size = (uint)Marshal.SizeOf<BitmapInfo>() - sizeof(uint),
                Width = bounds.Width,
                Height = -bounds.Height, // top-down, 32-bit BGRx
                Planes = 1,
                BitCount = 32
            };
            var bitmap = CreateDIBSection(dc, ref info, 0, out var pixels, 0, 0);
            if (bitmap == 0) throw Win32Failure("CreateDIBSection");
            try
            {
                var previous = SelectObject(dc, bitmap);
                if (previous == 0 || previous == -1) throw Win32Failure("SelectObject");
                try
                {
                    if (!PatBlt(dc, 0, 0, bounds.Width, bounds.Height, 0x42)) throw Win32Failure("PatBlt"); // BLACKNESS
                    token.ThrowIfCancellationRequested();
                    if (Observe(window.Handle) != window)
                        throw Failure(SurfaceError.Stale, "The window changed before capture.");
                    if (!PrintWindow(window.Handle, dc, 0)) throw Win32Failure("PrintWindow");
                    token.ThrowIfCancellationRequested();
                    CheckCapturePermission(window.Handle);
                    if (!GdiFlush()) throw Win32Failure("GdiFlush");
                    return SurfacePng.Encode(bounds.Width, bounds.Height, maxBytes,
                        (y, row) => Marshal.Copy(pixels + checked(y * bounds.Width * 4), row, 0, row.Length), token);
                }
                finally { SelectObject(dc, previous); }
            }
            finally { DeleteObject(bitmap); }
        }
        finally { DeleteDC(dc); }
    }

    private static void CheckCapturePermission(nint window)
    {
        var allowed = GetWindowDisplayAffinity(window, out var affinity);
        if (allowed ? affinity != 0 : Marshal.GetLastPInvokeError() == 5)
            throw Failure(SurfaceError.Permission, "Windows restricts capture of the selected window.");
    }

    public nint Foreground() => GetForegroundWindow();

    public bool KeyboardTargets(nint window)
    {
        var info = new GuiThreadInfo { Size = Marshal.SizeOf<GuiThreadInfo>() };
        return GetGUIThreadInfo(0, ref info) && info.Focus != 0 && GetAncestor(info.Focus, 2) == window;
    }

    public bool PointerTargets(nint window, SurfacePoint screen)
    {
        using var dpi = new PhysicalCoordinates();
        var hit = WindowFromPoint(screen);
        var info = new GuiThreadInfo { Size = Marshal.SizeOf<GuiThreadInfo>() };
        return hit != 0 && GetAncestor(hit, 2) == window && GetGUIThreadInfo(0, ref info)
            && (info.Capture == 0 || GetAncestor(info.Capture, 2) == window);
    }

    public void SetCursor(SurfacePoint screen)
    {
        using var dpi = new PhysicalCoordinates();
        if (!SetCursorPos(screen.X, screen.Y)) throw Win32Failure("SetCursorPos");
    }

    public bool CursorAt(SurfacePoint screen)
    {
        using var dpi = new PhysicalCoordinates();
        if (!GetCursorPos(out var point)) throw Win32Failure("GetCursorPos");
        return point == screen;
    }

    public bool IsDown(ushort virtualKey) => GetAsyncKeyState(virtualKey) < 0;

    public int Send(NativeInput[] events) => (int)SendInput((uint)events.Length, events, Marshal.SizeOf<NativeInput>());

    private static SurfaceException Win32Failure(string operation, SurfaceError fallback = SurfaceError.Unavailable) =>
        Failure(Marshal.GetLastPInvokeError() == 5 ? SurfaceError.Permission : fallback, $"{operation} was refused by Windows.");

    private readonly struct PhysicalCoordinates : IDisposable
    {
        private readonly nint previous;
        public PhysicalCoordinates()
        {
            previous = SetThreadDpiAwarenessContext(-4); // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
            if (previous == 0) throw Win32Failure("SetThreadDpiAwarenessContext");
        }
        public void Dispose() => SetThreadDpiAwarenessContext(previous);
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct FileTime
    {
        private uint low, high;
        internal readonly ulong Value => ((ulong)high << 32) | low;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct Rect { internal int Left, Top, Right, Bottom; }

    [StructLayout(LayoutKind.Sequential)]
    private struct GuiThreadInfo
    {
        internal int Size, Flags;
        internal nint Active, Focus, Capture, MenuOwner, MoveSize, Caret;
        internal Rect CaretBounds;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct BitmapInfo
    {
        internal uint Size;
        internal int Width, Height;
        internal ushort Planes, BitCount;
        internal uint Compression, SizeImage;
        internal int XPelsPerMeter, YPelsPerMeter;
        internal uint ColorsUsed, ColorsImportant, Colors;
    }

    private delegate bool EnumWindow(nint window, nint argument);

    [DllImport("user32.dll", SetLastError = true)] private static extern bool EnumWindows(EnumWindow callback, nint argument);
    [DllImport("user32.dll")] private static extern nint GetAncestor(nint window, uint flag);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(nint window);
    [DllImport("user32.dll")] private static extern bool IsIconic(nint window);
    [DllImport("dwmapi.dll")] private static extern int DwmGetWindowAttribute(nint window, int attribute, out int value, int size);
    [DllImport("user32.dll")] private static extern bool GetWindowRect(nint window, out Rect rect);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(nint window, out uint process);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern SafeProcessHandle OpenProcess(uint access, bool inherit, uint process);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GetProcessTimes(SafeProcessHandle process, out FileTime creation, out FileTime exit, out FileTime kernel, out FileTime user);
    [DllImport("user32.dll", SetLastError = true)] private static extern nint SetThreadDpiAwarenessContext(nint context);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetWindowDisplayAffinity(nint window, out uint affinity);
    [DllImport("gdi32.dll", SetLastError = true)] private static extern nint CreateCompatibleDC(nint dc);
    [DllImport("gdi32.dll", SetLastError = true)]
    private static extern nint CreateDIBSection(nint dc, ref BitmapInfo info, uint usage, out nint pixels, nint section, uint offset);
    [DllImport("gdi32.dll", SetLastError = true)] private static extern nint SelectObject(nint dc, nint value);
    [DllImport("gdi32.dll", SetLastError = true)]
    private static extern bool PatBlt(nint dc, int x, int y, int width, int height, uint operation);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool PrintWindow(nint window, nint dc, uint flags);
    [DllImport("gdi32.dll", SetLastError = true)] private static extern bool GdiFlush();
    [DllImport("gdi32.dll")] private static extern bool DeleteObject(nint obj);
    [DllImport("gdi32.dll")] private static extern bool DeleteDC(nint dc);
    [DllImport("user32.dll")] private static extern nint GetForegroundWindow();
    [DllImport("user32.dll")] private static extern nint WindowFromPoint(SurfacePoint point);
    [DllImport("user32.dll")] private static extern bool GetGUIThreadInfo(uint thread, ref GuiThreadInfo info);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetCursorPos(out SurfacePoint point);
    [DllImport("user32.dll")] private static extern short GetAsyncKeyState(int virtualKey);
    [DllImport("user32.dll", SetLastError = true)] private static extern uint SendInput(uint count, NativeInput[] events, int size);
}
