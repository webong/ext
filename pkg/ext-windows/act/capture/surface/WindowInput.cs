using System.Buffers;
using System.Runtime.InteropServices;
using System.Text;

namespace Webong.Ext.System.Windows;

public sealed partial class WindowSurface
{
    private static readonly Dictionary<string, ushort> Keys = new(StringComparer.OrdinalIgnoreCase)
    {
        ["enter"] = 0x0d, ["tab"] = 0x09, ["escape"] = 0x1b, ["space"] = 0x20,
        ["left"] = 0x25, ["up"] = 0x26, ["right"] = 0x27, ["down"] = 0x28,
        ["backspace"] = 0x08, ["delete"] = 0x2e
    };

    private static readonly Dictionary<string, ushort> Modifiers = new(StringComparer.OrdinalIgnoreCase)
    {
        ["command"] = 0x5b, ["control"] = 0x11, ["alt"] = 0x12, ["shift"] = 0x10
    };
    private static readonly ushort[] MouseKeys = [1, 2, 4, 5, 6];

    public void Move(WindowReference target, SurfacePoint point, CancellationToken cancellationToken = default) =>
        RunInput(target, default, cancellationToken, token => Position(target, point, token));

    public void Click(WindowReference target, SurfacePoint point, PointerButton button = PointerButton.Left,
        CancellationToken cancellationToken = default)
    {
        var (key, down, up) = Button(button);
        RunInput(target, default, cancellationToken, token =>
        {
            Position(target, point, token);
            RequireReleased(key);
            SendBalanced([down, up]);
        });
    }

    public void Drag(WindowReference target, SurfacePoint start, SurfacePoint end, CancellationToken cancellationToken = default)
    {
        RunInput(target, default, cancellationToken, token =>
        {
            var (key, down, up) = Button(PointerButton.Left);
            CheckPoint(target, end, token);
            Position(target, start, token);
            RequireReleased(key);
            SendSingle(down);
            try { Position(target, end, token, [PointerButton.Left]); }
            finally { Release(up); }
        });
    }

    public void Scroll(WindowReference target, SurfacePoint point, int deltaX, int deltaY,
        CancellationToken cancellationToken = default)
    {
        if (deltaX is < -1_000_000 or > 1_000_000 || deltaY is < -1_000_000 or > 1_000_000)
            throw new ArgumentOutOfRangeException(nameof(deltaX), "Scroll deltas must be within ±1,000,000.");
        RunInput(target, default, cancellationToken, token =>
        {
            Position(target, point, token);
            if (deltaY != 0) SendSingle(NativeInput.Mouse(0x0800, deltaY * 120));
            if (deltaX != 0)
            {
                CheckPoint(target, point, token);
                if (!native.CursorAt(ScreenPoint(target.Bounds, point))) throw FocusFailure();
                SendSingle(NativeInput.Mouse(0x1000, deltaX * 120));
            }
        });
    }

    public void TypeText(WindowReference target, string text, InputOptions? options = null,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(text);
        options ??= new();
        var maxRunes = Limit(options.MaxTextRunes, 4096, 1_000_000, nameof(options.MaxTextRunes));
        if (text.Length > maxRunes * 2) throw Failure(SurfaceError.LimitExceeded, "The text exceeds the input rune limit.");
        RunInput(target, options.Timeout, cancellationToken, token =>
        {
            int count = 0;
            for (int offset = 0; offset < text.Length;)
            {
                token.ThrowIfCancellationRequested();
                if (Rune.DecodeFromUtf16(text.AsSpan(offset), out _, out var consumed) != OperationStatus.Done)
                    throw new ArgumentException("The text contains invalid UTF-16.", nameof(text));
                if (++count > maxRunes) throw Failure(SurfaceError.LimitExceeded, "The text exceeds the input rune limit.");
                offset += consumed;
            }
            // Revalidate between scalars. A UTF-16 surrogate pair is sent together.
            for (int offset = 0; offset < text.Length;)
            {
                CheckKeyboardFocus(target, token);
                var first = NativeInput.Key(0, text[offset], 4);
                if (char.IsHighSurrogate(text[offset++]))
                {
                    var second = NativeInput.Key(0, text[offset++], 4);
                    SendBalanced([first, first.Release, second, second.Release]);
                }
                else SendBalanced([first, first.Release]);
            }
        });
    }

    public void PressKey(WindowReference target, string key, IReadOnlyList<string>? modifiers = null,
        CancellationToken cancellationToken = default)
    {
        if (key is null || !Keys.TryGetValue(key.Trim(), out var code))
            throw new ArgumentException("Unsupported keyboard key.", nameof(key));
        if (modifiers is { Count: > 4 }) throw new ArgumentException("At most four modifiers are supported.", nameof(modifiers));
        var pressed = new List<NativeInput>();
        foreach (var modifier in modifiers ?? [])
        {
            if (modifier is null || !Modifiers.TryGetValue(modifier.Trim(), out var value)
                || pressed.Any(item => item.KeyCode == value))
                throw new ArgumentException("Unsupported or repeated keyboard modifier.", nameof(modifiers));
            pressed.Add(NativeInput.Key(value));
        }
        pressed.Add(NativeInput.Key(code, flags: code is >= 0x25 and <= 0x28 or 0x2e ? 1u : 0u));
        NativeInput[] events = [..pressed, ..Enumerable.Reverse(pressed).Select(item => item.Release)];
        RunInput(target, default, cancellationToken, token =>
        {
            RequireReleased(code);
            CheckKeyboardFocus(target, token);
            SendBalanced(events);
        });
    }

    /// <summary>Start a serialized hold session for the selected foreground window.</summary>
    public PointerSession OpenPointerSession(WindowReference target, TimeSpan maxDuration = default,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(target);
        if (maxDuration < TimeSpan.Zero || maxDuration > TimeSpan.FromHours(1))
            throw new ArgumentOutOfRangeException(nameof(maxDuration));
        using var deadline = Deadline(cancellationToken, default);
        lock (InputGate)
        {
            RequireNoSession();
            CheckFocus(target, deadline.Token);
            var session = new PointerSession(this, target);
            ActiveSession = session;
            session.Arm(maxDuration == default ? TimeSpan.FromMinutes(5) : maxDuration, cancellationToken);
            return session;
        }
    }

    private void RunInput(WindowReference target, TimeSpan timeout, CancellationToken cancellationToken,
        Action<CancellationToken> action)
    {
        ArgumentNullException.ThrowIfNull(target);
        using var deadline = Deadline(cancellationToken, timeout);
        lock (InputGate)
        {
            deadline.Token.ThrowIfCancellationRequested();
            RequireNoSession();
            action(deadline.Token);
        }
    }

    private static void RequireNoSession()
    {
        if (ActiveSession is not null) throw Failure(SurfaceError.InputBusy, "A pointer session already owns Ext input.");
    }

    internal void Position(WindowReference target, SurfacePoint point, CancellationToken token,
        IReadOnlyList<PointerButton>? held = null)
    {
        var screen = CheckPoint(target, point, token);
        foreach (var key in MouseKeys)
            if (held is null || !held.Any(button => Button(button).Key == key))
                RequireReleased(key);
        native.SetCursor(screen);
        CheckPoint(target, point, token);
        if (!native.CursorAt(screen)) throw FocusFailure();
    }

    private SurfacePoint CheckPoint(WindowReference target, SurfacePoint point, CancellationToken token)
    {
        var bounds = CheckFocus(target, token).Bounds;
        var screen = ScreenPoint(bounds, point);
        if (!native.PointerTargets(target.Native.Handle, screen)) throw FocusFailure();
        return screen;
    }

    private static SurfacePoint ScreenPoint(SurfaceBounds bounds, SurfacePoint point)
    {
        if (point.X < 0 || point.X >= bounds.Width || point.Y < 0 || point.Y >= bounds.Height)
            throw new ArgumentOutOfRangeException(nameof(point), "Pointer coordinates must be inside the selected window.");
        return new(checked(bounds.X + point.X), checked(bounds.Y + point.Y));
    }

    internal NativeWindow CheckFocus(WindowReference target, CancellationToken token)
    {
        token.ThrowIfCancellationRequested();
        var selected = Revalidate(target);
        if (native.Foreground() != selected.Handle) throw FocusFailure();
        token.ThrowIfCancellationRequested();
        return selected;
    }

    private void CheckKeyboardFocus(WindowReference target, CancellationToken token)
    {
        foreach (var key in Modifiers.Values) RequireReleased(key);
        RequireReleased(0x5c); // right Windows key
        var selected = CheckFocus(target, token);
        if (!native.KeyboardTargets(selected.Handle)) throw FocusFailure();
    }

    private static SurfaceException FocusFailure() =>
        Failure(SurfaceError.NotFocused, "Input cannot be directed to the selected foreground window.");

    internal static (ushort Key, NativeInput Down, NativeInput Up) Button(PointerButton button) => button switch
    {
        PointerButton.Left => (1, NativeInput.Mouse(2), NativeInput.Mouse(4)),
        PointerButton.Right => (2, NativeInput.Mouse(8), NativeInput.Mouse(16)),
        PointerButton.Middle => (4, NativeInput.Mouse(32), NativeInput.Mouse(64)),
        _ => throw new ArgumentOutOfRangeException(nameof(button))
    };

    internal void RequireReleased(ushort key)
    {
        if (native.IsDown(key)) throw Failure(SurfaceError.InputBusy, "The requested button or key is already held.");
    }

    internal void SendSingle(NativeInput input)
    {
        if (native.Send([input]) != 1)
            throw Failure(SurfaceError.Unavailable, "Windows refused input.");
    }

    internal void Release(NativeInput input)
    {
        for (int attempt = 0; attempt < 3; attempt++)
            if (native.Send([input]) == 1) return;
        throw Failure(SurfaceError.Unavailable, "Windows refused release of held input.");
    }

    private void SendBalanced(NativeInput[] events)
    {
        int accepted = native.Send(events);
        if (accepted == events.Length) return;
        var held = new List<NativeInput>();
        for (int index = 0; index < accepted; index++)
        {
            var item = events[index];
            // Each balanced batch has nested down/up events.
            if (item.IsRelease) held.RemoveAt(held.Count - 1);
            else held.Add(item);
        }
        ReleaseAll(Enumerable.Reverse(held).Select(item => item.Release));
        throw Failure(SurfaceError.Unavailable, "Windows refused part or all of the input batch.");
    }

    internal void ReleaseAll(IEnumerable<NativeInput> releases)
    {
        SurfaceException? failure = null;
        foreach (var input in releases)
        {
            try { Release(input); }
            catch (SurfaceException ex) { failure ??= ex; }
        }
        if (failure is not null) throw failure;
    }
}

/// <summary>Holds pointer buttons until released, canceled, expired, or disposed.</summary>
public sealed class PointerSession : IDisposable
{
    private static readonly TimeSpan NoSchedule = Timeout.InfiniteTimeSpan;
    private static readonly TimeSpan FocusCheckInterval = TimeSpan.FromMilliseconds(100);
    private readonly WindowSurface surface;
    private readonly WindowReference target;
    private readonly List<PointerButton> held = [];
    private readonly TaskCompletionSource completion = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private Timer? expiry;
    private Timer? focus;
    private CancellationTokenRegistration cancellation;
    private bool closed;
    private SurfaceException? releaseFailure;

    internal PointerSession(WindowSurface surface, WindowReference target) => (this.surface, this.target) = (surface, target);

    /// <summary>Completes after cleanup and reports if Windows refused a release.</summary>
    public Task Completion => completion.Task;

    internal void Arm(TimeSpan duration, CancellationToken token)
    {
        expiry = new Timer(_ => End(), null, duration, NoSchedule);
        focus = new Timer(_ => CheckHeldTarget(), null, NoSchedule, NoSchedule);
        cancellation = token.UnsafeRegister(_ => End(), null);
        if (closed) cancellation.Unregister();
    }

    public void Down(SurfacePoint point, PointerButton button = PointerButton.Left, CancellationToken cancellationToken = default) =>
        Run(cancellationToken, token =>
        {
            var (key, down, _) = WindowSurface.Button(button);
            if (held.Contains(button)) throw new InvalidOperationException("The pointer button is already held by this session.");
            surface.Position(target, point, token, held);
            surface.RequireReleased(key);
            surface.SendSingle(down);
            held.Add(button);
            if (held.Count == 1) focus!.Change(FocusCheckInterval, FocusCheckInterval);
        });

    public void Move(SurfacePoint point, CancellationToken cancellationToken = default) =>
        Run(cancellationToken, token => surface.Position(target, point, token, held));

    public void Up(SurfacePoint point, PointerButton button = PointerButton.Left, CancellationToken cancellationToken = default) =>
        Run(cancellationToken, token =>
        {
            var (_, _, up) = WindowSurface.Button(button);
            if (!held.Contains(button)) throw new InvalidOperationException("The pointer button is not held by this session.");
            surface.Position(target, point, token, held);
            surface.SendSingle(up);
            held.Remove(button);
            if (held.Count == 0) focus!.Change(NoSchedule, NoSchedule);
        });

    private void Run(CancellationToken token, Action<CancellationToken> action)
    {
        using var deadline = WindowSurface.Deadline(token, default);
        lock (WindowSurface.InputGate)
        {
            ObjectDisposedException.ThrowIf(closed, this);
            try { deadline.Token.ThrowIfCancellationRequested(); action(deadline.Token); }
            catch (Exception error) when (error is OperationCanceledException or SurfaceException)
            {
                End();
                throw;
            }
        }
    }

    private void CheckHeldTarget()
    {
        lock (WindowSurface.InputGate)
        {
            if (closed || held.Count == 0) return;
            try { surface.CheckFocus(target, default); }
            catch (SurfaceException) { End(); }
        }
    }

    private void End()
    {
        lock (WindowSurface.InputGate)
        {
            if (closed) return;
            closed = true;
            expiry?.Dispose();
            focus?.Dispose();
            cancellation.Unregister();
            try
            {
                // Release only buttons we pressed, using their OS-global up event
                // without moving the pointer or requiring the old focus.
                surface.ReleaseAll(Enumerable.Reverse(held).Select(button => WindowSurface.Button(button).Up));
                completion.TrySetResult();
            }
            catch (SurfaceException error)
            {
                releaseFailure = error;
                completion.TrySetException(error);
            }
            finally { WindowSurface.ActiveSession = null; }
        }
    }

    public void Dispose()
    {
        End();
        if (releaseFailure is not null) throw releaseFailure;
    }
}

[StructLayout(LayoutKind.Sequential)]
internal struct NativeInput
{
    private uint kind;
    private Payload data;
    internal readonly ushort KeyCode => data.Key.Code;

    internal readonly bool IsRelease => kind == 1
        ? (data.Key.Flags & 2) != 0
        : (data.Mouse.Flags & (4 | 16 | 64)) != 0;

    internal readonly NativeInput Release => kind == 1
        ? Key(data.Key.Code, data.Key.Scan, data.Key.Flags | 2)
        : Mouse(data.Mouse.Flags << 1);

    internal static NativeInput Key(ushort code, ushort scan = 0, uint flags = 0) =>
        new() { kind = 1, data = new() { Key = new() { Code = code, Scan = scan, Flags = flags } } };

    internal static NativeInput Mouse(uint flags, int value = 0) =>
        new() { data = new() { Mouse = new() { Flags = flags, Value = value } } };

    [StructLayout(LayoutKind.Explicit)]
    private struct Payload
    {
        [FieldOffset(0)] internal MouseInput Mouse;
        [FieldOffset(0)] internal KeyboardInput Key;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct MouseInput
    {
        internal int X, Y, Value;
        internal uint Flags, Time;
        internal nuint Extra;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct KeyboardInput
    {
        internal ushort Code, Scan;
        internal uint Flags, Time;
        internal nuint Extra;
    }
}
