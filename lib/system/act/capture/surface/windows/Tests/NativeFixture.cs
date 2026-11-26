using System.Collections.Concurrent;
using System.Runtime.InteropServices;
using System.Text;
using Webong.Ext.System.Windows;

internal static class NativeFixture
{
    internal static async Task Run(bool required)
    {
        if (!Environment.UserInteractive)
        {
            Skip("the current Windows session is not interactive", required);
            return;
        }

        using var fixture = new FixtureWindows();
        var (first, second, editor) = await fixture.Ready.WaitAsync(TimeSpan.FromSeconds(5));
        if (first == 0 || second == 0 || editor == 0)
        {
            Skip("fixture windows could not be created", required);
            return;
        }

        var surface = new WindowSurface();
        var selected = surface.ObserveWindow(first);
        if (!surface.ObserveWindows().Windows.Any(window => window.Id == selected.Id))
            throw new Exception("The real fixture window was absent from enumeration.");

        var image = await surface.CaptureAsync(selected);
        if (image.Width != selected.Bounds.Width || image.Height != selected.Bounds.Height
            || !image.Data.AsSpan(0, 8).SequenceEqual(new byte[] { 137, 80, 78, 71, 13, 10, 26, 10 }))
            throw new Exception("Invalid capture of the real fixture window.");
        await Expect(() => surface.CaptureAsync(selected, new(MaxPixels: 1)), SurfaceError.LimitExceeded);
        await Expect(() => surface.CaptureAsync(selected, new(MaxBytes: 32)), SurfaceError.LimitExceeded);

        if (fixture.Invoke(() => SetWindowDisplayAffinity(first, 1)))
        {
            try { await Expect(() => surface.CaptureAsync(selected), SurfaceError.Permission); }
            finally { fixture.Invoke(() => SetWindowDisplayAffinity(first, 0)); }
        }
        else Skip("Windows display affinity is unavailable", required);

        var originalFocus = GetForegroundWindow();
        GetCursorPos(out var originalCursor);
        try
        {
            var point = new SurfacePoint(selected.Bounds.Width / 2, selected.Bounds.Height / 2);
            if (fixture.Focus(first, second))
            {
                fixture.Invoke(() => SetFocus(editor));
                surface.TypeText(selected, "Ext");
                await WaitForText(editor, "Ext");

                using var cancel = new CancellationTokenSource();
                using (var session = surface.OpenPointerSession(selected, cancellationToken: cancel.Token))
                {
                    session.Down(point);
                    await WaitButton(down: true);
                    cancel.Cancel();
                    await session.Completion.WaitAsync(TimeSpan.FromSeconds(5));
                    await WaitButton(down: false);
                }

                using (var session = surface.OpenPointerSession(selected))
                {
                    session.Down(point);
                    await WaitButton(down: true);
                }
                await WaitButton(down: false);

                if (!fixture.Focus(second, first))
                    throw new Exception("The real fixture could not transfer its foreground window.");
                await Expect(() => { surface.Move(selected, point); return Task.CompletedTask; }, SurfaceError.NotFocused);
            }
            else Skip("Windows did not grant the test windows foreground input", required);
        }
        finally
        {
            if (originalFocus != 0) SetForegroundWindow(originalFocus);
            SetCursorPos(originalCursor.X, originalCursor.Y);
        }

        if (!fixture.Invoke(() => MoveWindow(first, 60, 70, 220, 130, true)))
            throw new Exception("Moving the real fixture window failed.");
        await Expect(() => surface.CaptureAsync(selected), SurfaceError.Stale);
        var moved = surface.ObserveWindow(first);
        if (!fixture.Invoke(() => DestroyWindow(first))) throw new Exception("Closing the real fixture window failed.");
        await Expect(() => surface.CaptureAsync(moved), SurfaceError.Stale);
        Console.WriteLine("PASS real Windows observation, capture, pixel and byte bounds, and stale references");
    }

    private static async Task WaitForText(nint editor, string expected)
    {
        var text = new StringBuilder(32);
        for (var attempt = 0; attempt < 100; attempt++)
        {
            GetWindowTextW(editor, text, text.Capacity);
            if (text.ToString() == expected) return;
            await Task.Delay(10);
        }
        throw new Exception("Only the selected fixture's edit control should have received keyboard text.");
    }

    private static void Skip(string why, bool required)
    {
        if (required) throw new Exception(why);
        Console.WriteLine($"SKIP {why}");
    }

    private static async Task WaitButton(bool down)
    {
        for (var attempt = 0; attempt < 100; attempt++)
        {
            if ((GetAsyncKeyState(1) < 0) == down) return;
            await Task.Delay(10);
        }
        throw new Exception($"The native pointer button state did not become {(down ? "pressed" : "released")}.");
    }

    private static async Task Expect(Func<Task> operation, SurfaceError code)
    {
        try { await operation(); }
        catch (SurfaceException error) when (error.Code == code) { return; }
        throw new Exception($"Expected a real native {code} result.");
    }

    private sealed class FixtureWindows : IDisposable
    {
        private readonly BlockingCollection<Action> commands = new();
        private readonly TaskCompletionSource<(nint, nint, nint)> ready = new(TaskCreationOptions.RunContinuationsAsynchronously);
        private readonly Thread thread;
        internal Task<(nint, nint, nint)> Ready => ready.Task;

        internal FixtureWindows()
        {
            thread = new Thread(Pump) { IsBackground = true };
            thread.Start();
        }

        internal bool Focus(nint window, nint other)
        {
            Invoke(() =>
            {
                SetForegroundWindow(window);
                if (GetForegroundWindow() != window && GetForegroundWindow() == other) SetForegroundWindow(window);
                return true;
            });
            return SpinWait.SpinUntil(() => GetForegroundWindow() == window, TimeSpan.FromSeconds(1));
        }

        internal T Invoke<T>(Func<T> action)
        {
            var result = new TaskCompletionSource<T>(TaskCreationOptions.RunContinuationsAsynchronously);
            commands.Add(() =>
            {
                try { result.SetResult(action()); }
                catch (Exception error) { result.SetException(error); }
            });
            return result.Task.WaitAsync(TimeSpan.FromSeconds(5)).GetAwaiter().GetResult();
        }

        private void Pump()
        {
            const uint visibleOverlapped = 0x10cf0000;
            var first = CreateWindowExW(0, "STATIC", "Ext surface fixture A", visibleOverlapped,
                24, 24, 200, 140, 0, 0, 0, 0);
            var second = CreateWindowExW(0, "STATIC", "Ext surface fixture B", visibleOverlapped,
                300, 24, 200, 140, 0, 0, 0, 0);
            var editor = CreateWindowExW(0, "EDIT", "", 0x50800000, 8, 8, 120, 24, first, 0, 0, 0);
            ready.SetResult((first, second, editor));
            try
            {
                while (!commands.IsCompleted)
                {
                    if (commands.TryTake(out var command, 10)) command();
                    while (PeekMessageW(out var msg, 0, 0, 0, 1))
                    {
                        TranslateMessage(ref msg);
                        DispatchMessageW(ref msg);
                    }
                }
            }
            finally
            {
                if (IsWindow(first)) DestroyWindow(first);
                if (IsWindow(second)) DestroyWindow(second);
            }
        }

        public void Dispose()
        {
            commands.CompleteAdding();
            if (!thread.Join(TimeSpan.FromSeconds(5))) throw new Exception("Fixture window thread did not exit.");
            commands.Dispose();
        }
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct Msg
    {
        internal nint Window;
        internal uint Id;
        internal nuint WParam;
        internal nint LParam;
        internal uint Time;
        internal SurfacePoint Point;
        internal uint Private;
    }

    [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
    private static extern nint CreateWindowExW(uint extra, string windowClass, string title, uint style,
        int x, int y, int width, int height, nint parent, nint menu, nint instance, nint param);
    [DllImport("user32.dll")] private static extern bool IsWindow(nint window);
    [DllImport("user32.dll")] private static extern bool DestroyWindow(nint window);
    [DllImport("user32.dll")] private static extern bool PeekMessageW(out Msg msg, nint window, uint min, uint max, uint remove);
    [DllImport("user32.dll")] private static extern bool TranslateMessage(ref Msg msg);
    [DllImport("user32.dll")] private static extern nint DispatchMessageW(ref Msg msg);
    [DllImport("user32.dll")] private static extern nint GetForegroundWindow();
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(nint window);
    [DllImport("user32.dll")] private static extern nint SetFocus(nint window);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
    private static extern int GetWindowTextW(nint window, StringBuilder text, int count);
    [DllImport("user32.dll")] private static extern bool GetCursorPos(out SurfacePoint point);
    [DllImport("user32.dll")] private static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] private static extern short GetAsyncKeyState(int key);
    [DllImport("user32.dll")] private static extern bool MoveWindow(nint window, int x, int y, int width, int height, bool repaint);
    [DllImport("user32.dll")] private static extern bool SetWindowDisplayAffinity(nint window, uint affinity);
}
