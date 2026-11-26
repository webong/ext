using System.Globalization;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;

namespace Webong.Ext.System.Windows;

[StructLayout(LayoutKind.Sequential)]
public readonly record struct SurfacePoint(int X, int Y);
public readonly record struct SurfaceBounds(int X, int Y, int Width, int Height);

/// <summary>A point-in-time reference to a top-level window in the local desktop.</summary>
public sealed class WindowReference
{
    internal NativeWindow Native { get; }
    internal WindowReference(NativeWindow native) => Native = native;
    public int ProcessId => checked((int)Native.ProcessId);
    public string ProcessStartId => Native.ProcessStart.ToString(CultureInfo.InvariantCulture);
    public SurfaceBounds Bounds => Native.Bounds;
    public string Id
    {
        get
        {
            var value = FormattableString.Invariant($"window\0{ProcessId}\0{ProcessStartId}\0{(nuint)Native.Handle}");
            var digest = SHA256.HashData(Encoding.UTF8.GetBytes(value));
            return "window:" + Convert.ToHexString(digest.AsSpan(0, 12)).ToLowerInvariant();
        }
    }
}

public sealed record WindowSnapshot(IReadOnlyList<WindowReference> Windows, bool Truncated);
public sealed record SurfaceCapture(int Width, int Height, byte[] Data, DateTimeOffset CapturedAt)
{
    public string Format => "image/png";
}
public sealed record CaptureOptions(int MaxPixels = 0, int MaxBytes = 0, TimeSpan Timeout = default);
public sealed record InputOptions(int MaxTextRunes = 0, TimeSpan Timeout = default);

public enum SurfaceError
{
    Unavailable,
    Permission,
    Stale,
    NotFocused,
    LimitExceeded,
    InputBusy
}

public sealed class SurfaceException : Exception
{
    public SurfaceError Code { get; }
    internal SurfaceException(SurfaceError code, string message) : base(message) => Code = code;
}

public enum PointerButton { Left, Right, Middle }

/// <summary>Observes local Windows surfaces and operates on an explicitly selected reference.</summary>
public sealed partial class WindowSurface
{
    private static readonly SemaphoreSlim CaptureSlot = new(1);
    internal static readonly object InputGate = new();
    internal static PointerSession? ActiveSession;
    private readonly IWindowNative native;

    public WindowSurface() : this(new Win32Surface())
    {
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 15063))
            throw new PlatformNotSupportedException("Windows 10 version 1703 or later is required.");
    }

    internal WindowSurface(IWindowNative native) => this.native = native;

    /// <summary>Observe a caller-selected HWND. Coordinates are physical virtual-desktop pixels.</summary>
    public WindowReference ObserveWindow(nint window)
    {
        if (window == 0) throw new ArgumentException("A window handle is required.", nameof(window));
        return new(native.Observe(window));
    }

    /// <summary>Enumerate visible, identifiable top-level windows for the caller to select.</summary>
    public WindowSnapshot ObserveWindows(int maxWindows = 4096, CancellationToken cancellationToken = default)
    {
        if (maxWindows is < 1 or > 65_536) throw new ArgumentOutOfRangeException(nameof(maxWindows));
        var windows = new List<WindowReference>();
        var truncated = false;
        using var deadline = Deadline(cancellationToken, default);
        native.Enumerate(window =>
        {
            if (deadline.IsCancellationRequested) return false;
            NativeWindow current;
            try { current = native.Observe(window); }
            catch (SurfaceException e) when (e.Code is SurfaceError.Stale or SurfaceError.Permission) { return true; }
            if (windows.Count == maxWindows)
            {
                truncated = true;
                return false;
            }
            windows.Add(new(current));
            return true;
        });
        deadline.Token.ThrowIfCancellationRequested();
        return new(windows.AsReadOnly(), truncated);
    }

    /// <summary>Capture a selected window as PNG within pixel, byte and wait limits.</summary>
    public async Task<SurfaceCapture> CaptureAsync(WindowReference target, CaptureOptions? options = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(target);
        options ??= new();
        int pixels = Limit(options.MaxPixels, 16_777_216, 268_435_456, nameof(options.MaxPixels));
        int bytes = Limit(options.MaxBytes, 16 << 20, 1 << 30, nameof(options.MaxBytes));
        using var deadline = Deadline(cancellationToken, options.Timeout);
        var token = deadline.Token;
        token.ThrowIfCancellationRequested();
        if (!CaptureSlot.Wait(0))
            throw Failure(SurfaceError.Unavailable, "A Windows capture is already in progress.");

        // PrintWindow can block inside the target. Keep the slot and native buffers with
        // the worker until it returns, even when the caller's wait has expired.
        var worker = Task.Run(() =>
        {
            try
            {
                token.ThrowIfCancellationRequested();
                var selected = Revalidate(target);
                var bounds = selected.Bounds;
                if (bounds.Width > pixels / bounds.Height)
                    throw Failure(SurfaceError.LimitExceeded, "The window exceeds the capture pixel limit.");
                var data = native.Capture(selected, bytes, token);
                Revalidate(target);
                token.ThrowIfCancellationRequested();
                return new SurfaceCapture(bounds.Width, bounds.Height, data, DateTimeOffset.UtcNow);
            }
            finally { CaptureSlot.Release(); }
        });

        // Observe a worker failure even if the caller stops waiting.
        _ = worker.ContinueWith(task => _ = task.Exception, CancellationToken.None,
            TaskContinuationOptions.OnlyOnFaulted | TaskContinuationOptions.ExecuteSynchronously, TaskScheduler.Default);
        return await worker.WaitAsync(token).ConfigureAwait(false);
    }

    internal NativeWindow Revalidate(WindowReference target)
    {
        var current = native.Observe(target.Native.Handle);
        if (current != target.Native)
            throw Failure(SurfaceError.Stale, "The selected window identity or geometry changed.");
        return current;
    }

    internal static CancellationTokenSource Deadline(CancellationToken token, TimeSpan timeout)
    {
        if (timeout < TimeSpan.Zero || timeout.TotalMilliseconds > int.MaxValue)
            throw new ArgumentOutOfRangeException(nameof(timeout));
        var source = CancellationTokenSource.CreateLinkedTokenSource(token);
        source.CancelAfter(timeout == default ? TimeSpan.FromSeconds(5) : timeout);
        return source;
    }

    private static int Limit(int value, int defaultValue, int maximum, string name)
    {
        if (value < 0 || value > maximum) throw new ArgumentOutOfRangeException(name);
        return value == 0 ? defaultValue : value;
    }

    internal static SurfaceException Failure(SurfaceError code, string message) => new(code, message);
}

internal readonly record struct NativeWindow(nint Handle, uint ThreadId, uint ProcessId,
    ulong ProcessStart, SurfaceBounds Bounds);

internal interface IWindowNative
{
    NativeWindow Observe(nint handle);
    void Enumerate(Func<nint, bool> visit);
    byte[] Capture(NativeWindow window, int maxBytes, CancellationToken token);
    nint Foreground();
    bool KeyboardTargets(nint window);
    bool PointerTargets(nint window, SurfacePoint screen);
    void SetCursor(SurfacePoint screen);
    bool CursorAt(SurfacePoint screen);
    bool IsDown(ushort virtualKey);
    int Send(NativeInput[] events);
}
