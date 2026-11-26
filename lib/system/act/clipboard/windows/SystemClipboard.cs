using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;

namespace Webong.Ext.System.Windows;

/// <summary>
/// Bounds and permissions fixed by the embedding application before native
/// clipboard access. Formats are Win32 names: the four standard names this
/// library understands, or a registered format name. <see cref="Timeout"/> bounds each read and
/// write: five seconds when null, as Go's observers, and above zero and at most an hour.
/// </summary>
public sealed record ClipboardPolicy(
    IReadOnlyCollection<string> AllowedFormats,
    int MaxBytes = 16 * 1024 * 1024,
    int MaxItems = 64,
    int MaxFormats = 32,
    bool AllowRead = false,
    bool AllowWrite = false,
    TimeSpan? Timeout = null)
{
    /// <summary>The timeout of a policy that selects none.</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(5);

    public ClipboardPolicy Validate()
    {
        if (MaxBytes <= 0 || MaxBytes > 16 * 1024 * 1024 ||
            MaxItems < 1 || MaxItems > 64 ||
            MaxFormats < 1 || MaxFormats > 32 ||
            AllowedFormats is null || AllowedFormats.Count < 1 || AllowedFormats.Count > MaxFormats)
        {
            throw new ClipboardException("invalid", "clipboard needs bounded formats and byte/item ceilings");
        }
        if (!ClipboardBound.Valid(Timeout ?? DefaultTimeout))
        {
            throw new ClipboardException("invalid", "a clipboard timeout is above zero and at most an hour");
        }
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var format in AllowedFormats)
        {
            if (string.IsNullOrEmpty(format) || Encoding.UTF8.GetByteCount(format) > 256 || !seen.Add(format))
            {
                throw new ClipboardException("invalid", "clipboard formats must be unique names of at most 256 bytes");
            }
        }
        return this;
    }
}

public enum ClipboardErrorKind
{
    Invalid,
    Denied,
    Limit,
    Changed,
    /// <summary>Another process held the clipboard for as long as the library retried.</summary>
    Busy,
    /// <summary>A Win32 clipboard or global-memory call failed.</summary>
    Io,
    /// <summary>This session has no clipboard to open (see <see cref="SystemClipboard.Status"/>).</summary>
    Unavailable,
    /// <summary>The call ran past <see cref="ClipboardPolicy.Timeout"/>.</summary>
    Timeout,
    /// <summary>The caller's token was canceled.</summary>
    Canceled,
}

public sealed class ClipboardException : Exception
{
    public ClipboardErrorKind Kind { get; }

    /// <summary>The stable error code of res/system/testdata/codes.json.</summary>
    public string Code => Kind switch
    {
        ClipboardErrorKind.Denied => "denied",
        ClipboardErrorKind.Limit => "too_large",
        ClipboardErrorKind.Changed => "changed",
        ClipboardErrorKind.Busy => "busy",
        ClipboardErrorKind.Io => "io",
        ClipboardErrorKind.Unavailable => "unavailable",
        ClipboardErrorKind.Timeout => "timeout",
        ClipboardErrorKind.Canceled => "canceled",
        _ => "invalid",
    };

    public ClipboardException(ClipboardErrorKind kind, string message, Exception? inner = null)
        : base(message, inner)
    {
        Kind = kind;
    }

    public ClipboardException(string kind, string message, Exception? inner = null)
        : this(kind switch
        {
            "denied" => ClipboardErrorKind.Denied,
            "too_large" => ClipboardErrorKind.Limit,
            "changed" => ClipboardErrorKind.Changed,
            "busy" => ClipboardErrorKind.Busy,
            "io" => ClipboardErrorKind.Io,
            "unavailable" => ClipboardErrorKind.Unavailable,
            "timeout" => ClipboardErrorKind.Timeout,
            "canceled" => ClipboardErrorKind.Canceled,
            _ => ClipboardErrorKind.Invalid,
        }, message, inner)
    {
    }
}

/// <summary>
/// Whether this session has a clipboard, in the collection-status vocabulary every res/system
/// package shares (testdata/capability/v1, docs/adr-architecture.md rule 2). <c>complete</c>: there
/// is a clipboard, though another process may hold it for a moment (a read or write then fails
/// <c>busy</c>). <c>unavailable</c> with <c>no_device</c>: the process runs on a window station with
/// no interactive desktop, such as a service session, so there is no clipboard to open.
/// <c>unsupported</c> with <c>unsupported</c>: this is not Windows. The constructor refuses a state
/// and reason the capability fixture does not allow, with code <c>invalid</c>.
/// </summary>
public sealed record ClipboardStatus
{
    public string State { get; }
    public string? Reason { get; }

    public ClipboardStatus(string state, string? reason = null)
    {
        if (!Capability.Allowed(state, reason))
        {
            throw new ClipboardException(ClipboardErrorKind.Invalid, "clipboard status does not take that reason");
        }
        State = state;
        Reason = reason;
    }
}

/// <summary>
/// The time bound and cancellation of one call (docs/adr-architecture.md rule 4), as Go's context
/// deadline: it stops at its deadline and when the caller's token is canceled. The deadline is the
/// last clock tick inside the timeout, so the clock's resolution never lets a call run past it.
/// </summary>
internal readonly struct ClipboardBound
{
    /// <summary>The longest timeout a policy may select: one hour.</summary>
    internal static readonly TimeSpan MaximumTimeout = TimeSpan.FromHours(1);

    private readonly long deadline;
    private readonly CancellationToken token;

    internal ClipboardBound(TimeSpan timeout, CancellationToken token)
        : this(Stopwatch.GetTimestamp() + Math.Max((long)Math.Ceiling(timeout.TotalSeconds * Stopwatch.Frequency), 1) - 1, token)
    {
    }

    /// <summary>A bound ending at a Stopwatch timestamp, so the tests can make one that has passed.</summary>
    internal ClipboardBound(long deadline, CancellationToken token)
    {
        this.deadline = deadline;
        this.token = token;
    }

    internal static bool Valid(TimeSpan timeout) => timeout > TimeSpan.Zero && timeout <= MaximumTimeout;

    /// <summary>Throws <c>canceled</c> when the token is (judged first, as Go reports the parent context), else <c>timeout</c> past the deadline.</summary>
    internal void Check()
    {
        if (token.IsCancellationRequested)
        {
            throw new ClipboardException(ClipboardErrorKind.Canceled, "the clipboard call was canceled");
        }
        if (Stopwatch.GetTimestamp() >= deadline)
        {
            throw new ClipboardException(ClipboardErrorKind.Timeout, "the clipboard call ran past its timeout");
        }
    }
}

/// <summary>The state and reason pairs of testdata/capability/v1.</summary>
internal static class Capability
{
    internal static bool Allowed(string? state, string? reason) => state switch
    {
        "complete" or "partial" => reason is null,
        "permission-denied" => reason is null or "permission",
        "unsupported" => reason is "unsupported" or "tool_missing",
        "unavailable" => reason is null or "permission" or "no_device" or "tool_missing",
        _ => false,
    };
}

public sealed record ClipboardRepresentation(string Format, byte[] Data);

public sealed record ClipboardItem(IReadOnlyList<ClipboardRepresentation> Representations);

public sealed record ClipboardSnapshot(string Generation, IReadOnlyList<ClipboardItem> Items);

public sealed record ClipboardReceipt(string Generation, string Dispatch);

/// <summary>
/// The item rules every binding of the clipboard contract shares (testdata/clipboard/v1): the byte
/// ceiling is judged first, then the item count, then each item in order.
/// </summary>
internal static class ClipboardValidation
{
    internal static void ValidateItems(
        IReadOnlyList<ClipboardItem> items,
        int maxBytes,
        int maxItems,
        int maxFormats,
        IReadOnlyCollection<string> formats)
    {
        if (maxBytes <= 0 || maxBytes > 16 * 1024 * 1024)
        {
            throw new ClipboardException(ClipboardErrorKind.Invalid, "invalid clipboard byte ceiling");
        }
        if (items.Count > maxItems)
        {
            throw new ClipboardException(ClipboardErrorKind.Limit, "clipboard items exceed the policy");
        }
        var allowed = new HashSet<string>(formats, StringComparer.Ordinal);
        var total = 0L;
        foreach (var item in items)
        {
            if (item.Representations.Count < 1 || item.Representations.Count > maxFormats)
            {
                throw new ClipboardException(ClipboardErrorKind.Invalid,
                    "clipboard item needs between one and the format ceiling");
            }
            var seen = new HashSet<string>(StringComparer.Ordinal);
            foreach (var representation in item.Representations)
            {
                if (!allowed.Contains(representation.Format) || !seen.Add(representation.Format))
                {
                    throw new ClipboardException(ClipboardErrorKind.Denied,
                        "clipboard format is not allowed or is repeated");
                }
                total += representation.Data.Length;
                if (total > maxBytes)
                {
                    throw new ClipboardException(ClipboardErrorKind.Limit, "clipboard bytes exceed the policy");
                }
            }
        }
    }
}


/// <summary>
/// The Win32 calls the clipboard makes, so the tests can fail each one without a clipboard.
/// <see cref="Win32ClipboardApi"/> is the only implementation the library uses.
/// </summary>
internal interface IClipboardApi
{
    bool Open();
    void Close();
    bool Empty();
    uint EnumFormats(uint format);
    IntPtr GetData(uint format);
    IntPtr SetData(uint format, IntPtr memory);
    uint Register(string name);
    string? RegisteredName(uint format);
    uint Sequence();
    IntPtr Alloc(int bytes);
    void Free(IntPtr memory);
    IntPtr Lock(IntPtr memory);
    void Unlock(IntPtr memory);
    long Size(IntPtr memory);
    /// <summary>The last Win32 error of this thread, for the inner exception of an io failure.</summary>
    int LastError();
    /// <summary>Whether the process window station has an interactive desktop; null when unknown.</summary>
    bool? Interactive();
    void Sleep(int milliseconds);
}

/// <summary>
/// Bounded Win32 clipboard access for local consumers. It holds no history,
/// prompts for nothing, and retains no remote protocol or approval state.
/// Windows exposes one clipboard item with several formats, so a write with
/// more than one item fails instead of being flattened.
/// </summary>
/// <remarks>
/// Each read and write is bounded by <see cref="ClipboardPolicy.Timeout"/> and by the caller's
/// token (docs/adr-architecture.md rule 4): it throws <see cref="ClipboardException"/>
/// <c>timeout</c> or <c>canceled</c>, as Go's clipboard returns its context's error. The bound is
/// judged before each attempt to open the clipboard and between formats. A write that stops does so
/// before the clipboard is cleared, so it changes nothing; once cleared, it runs to the end.
/// </remarks>
public static class SystemClipboard
{
    private const uint CFText = 1;
    private const uint CFDib = 8;
    private const uint CFUnicodeText = 13;
    private const uint CFDibV5 = 17;

    /// <summary>OpenClipboard is tried this many times, ten milliseconds apart, before it is busy.</summary>
    internal const int OpenAttempts = 50;

    private static readonly Dictionary<string, uint> StandardFormats = new(StringComparer.Ordinal)
    {
        ["CF_TEXT"] = CFText,
        ["CF_DIB"] = CFDib,
        ["CF_UNICODETEXT"] = CFUnicodeText,
        ["CF_DIBV5"] = CFDibV5,
    };

    private static readonly Dictionary<uint, string> StandardNames = new()
    {
        [CFText] = "CF_TEXT",
        [CFDib] = "CF_DIB",
        [CFUnicodeText] = "CF_UNICODETEXT",
        [CFDibV5] = "CF_DIBV5",
    };

    private static readonly UTF8Encoding StrictUtf8 = new(false, true);

    /// <summary>
    /// Whether this session has a clipboard (<see cref="ClipboardStatus"/>). It opens nothing, so a
    /// clipboard another process holds right now is still <c>complete</c>.
    /// </summary>
    public static ClipboardStatus Status() => Status(OperatingSystem.IsWindows() ? Win32ClipboardApi.Instance : null);

    internal static ClipboardStatus Status(IClipboardApi? api)
    {
        if (api is null)
        {
            return new ClipboardStatus("unsupported", "unsupported");
        }
        return api.Interactive() == false ? new ClipboardStatus("unavailable", "no_device") : new ClipboardStatus("complete");
    }

    public static string Generation() => Generation(Win32ClipboardApi.Instance);

    internal static string Generation(IClipboardApi api) => $"windows:{api.Sequence()}";

    public static ClipboardSnapshot Read(ClipboardPolicy policy, CancellationToken cancellationToken = default) =>
        Read(policy, Win32ClipboardApi.Instance, cancellationToken);

    internal static ClipboardSnapshot Read(ClipboardPolicy policy, IClipboardApi api, CancellationToken cancellationToken = default) =>
        Read(policy, api, null, cancellationToken);

    /// <summary>A read under <paramref name="given"/>, or under the policy's timeout when it is null.</summary>
    internal static ClipboardSnapshot Read(ClipboardPolicy policy, IClipboardApi api, ClipboardBound? given, CancellationToken cancellationToken = default)
    {
        policy.Validate();
        if (!policy.AllowRead)
        {
            throw new ClipboardException(ClipboardErrorKind.Denied, "clipboard read is not allowed");
        }
        var bound = given ?? new ClipboardBound(policy.Timeout ?? ClipboardPolicy.DefaultTimeout, cancellationToken);
        OpenClipboard(api, bound);
        try
        {
            var before = Generation(api);
            var allowed = new HashSet<string>(policy.AllowedFormats, StringComparer.Ordinal);
            var representations = new List<ClipboardRepresentation>();
            var total = 0L;
            uint identifier = 0;
            while (true)
            {
                bound.Check();
                identifier = api.EnumFormats(identifier);
                if (identifier == 0)
                {
                    break;
                }
                var name = FormatName(api, identifier);
                if (!allowed.Contains(name))
                {
                    continue;
                }
                var handle = api.GetData(identifier);
                if (handle == IntPtr.Zero)
                {
                    throw Native(api, "cannot read a clipboard format");
                }
                var data = ReadGlobal(api, handle);
                total += data.Length;
                if (total > policy.MaxBytes)
                {
                    throw new ClipboardException(ClipboardErrorKind.Limit, "clipboard bytes exceed the policy");
                }
                if (identifier == CFUnicodeText)
                {
                    string text;
                    try
                    {
                        text = Encoding.Unicode.GetString(data);
                    }
                    catch (ArgumentException error)
                    {
                        throw new ClipboardException(ClipboardErrorKind.Invalid,
                            "clipboard text is not valid UTF-16", error);
                    }
                    var terminator = text.IndexOf('\0');
                    if (terminator >= 0)
                    {
                        text = text[..terminator];
                    }
                    data = Encoding.UTF8.GetBytes(text);
                }
                representations.Add(new ClipboardRepresentation(name, data));
            }
            if (representations.Count == 0)
            {
                // Denied, as in Go, Swift and Java: what the clipboard holds is outside the policy.
                throw new ClipboardException(ClipboardErrorKind.Denied,
                    "clipboard contains none of the allowed formats");
            }
            bound.Check();
            if (Generation(api) != before)
            {
                throw new ClipboardException(ClipboardErrorKind.Changed,
                    "clipboard changed while it was read");
            }
            var item = new ClipboardItem(representations);
            return new ClipboardSnapshot(before, new[] { item });
        }
        finally
        {
            api.Close();
        }
    }

    public static ClipboardReceipt Write(
        ClipboardPolicy policy,
        string expectedGeneration,
        IReadOnlyList<ClipboardItem> items,
        CancellationToken cancellationToken = default) => Write(policy, expectedGeneration, items, Win32ClipboardApi.Instance, null, cancellationToken);

    internal static ClipboardReceipt Write(
        ClipboardPolicy policy,
        string expectedGeneration,
        IReadOnlyList<ClipboardItem> items,
        IClipboardApi api,
        CancellationToken cancellationToken = default) => Write(policy, expectedGeneration, items, api, null, cancellationToken);

    /// <summary>A write under <paramref name="given"/>, or under the policy's timeout when it is null.</summary>
    internal static ClipboardReceipt Write(
        ClipboardPolicy policy,
        string expectedGeneration,
        IReadOnlyList<ClipboardItem> items,
        IClipboardApi api,
        ClipboardBound? given,
        CancellationToken cancellationToken = default)
    {
        policy.Validate();
        if (!policy.AllowWrite)
        {
            throw new ClipboardException(ClipboardErrorKind.Denied, "clipboard write is not allowed");
        }
        if (string.IsNullOrEmpty(expectedGeneration))
        {
            throw new ClipboardException(ClipboardErrorKind.Invalid, "expected generation is required");
        }
        items ??= Array.Empty<ClipboardItem>();
        if (items.Count > 1)
        {
            throw new ClipboardException(ClipboardErrorKind.Invalid,
                "the Windows clipboard holds one item");
        }
        ClipboardValidation.ValidateItems(items, policy.MaxBytes, policy.MaxItems, policy.MaxFormats, policy.AllowedFormats);
        var bound = given ?? new ClipboardBound(policy.Timeout ?? ClipboardPolicy.DefaultTimeout, cancellationToken);
        bound.Check();
        if (Generation(api) != expectedGeneration)
        {
            throw new ClipboardException(ClipboardErrorKind.Changed,
                "clipboard generation changed");
        }
        OpenClipboard(api, bound);
        try
        {
            if (Generation(api) != expectedGeneration)
            {
                throw new ClipboardException(ClipboardErrorKind.Changed,
                    "clipboard generation changed before the write");
            }
            // The last point a write can stop at: after this the clipboard is cleared.
            bound.Check();
            if (!api.Empty())
            {
                throw Native(api, "cannot clear the clipboard");
            }
            if (items.Count == 1)
            {
                foreach (var representation in items[0].Representations)
                {
                    var identifier = FormatIdentifier(api, representation.Format);
                    byte[] data;
                    if (representation.Format == "CF_UNICODETEXT")
                    {
                        try
                        {
                            data = Encoding.Unicode.GetBytes(StrictUtf8.GetString(representation.Data) + "\0");
                        }
                        catch (DecoderFallbackException error)
                        {
                            throw new ClipboardException(ClipboardErrorKind.Invalid,
                                "clipboard text is not valid UTF-8", error);
                        }
                    }
                    else
                    {
                        data = representation.Data;
                    }
                    WriteGlobal(api, identifier, data);
                }
            }
        }
        finally
        {
            api.Close();
        }
        // After CloseClipboard: closing a clipboard that holds text adds CF_LOCALE and moves the
        // sequence number on, so a generation read while it is open is already stale.
        return new ClipboardReceipt(Generation(api), "dispatched");
    }

    public static ClipboardReceipt Clear(ClipboardPolicy policy, string expectedGeneration, CancellationToken cancellationToken = default) =>
        Write(policy, expectedGeneration, Array.Empty<ClipboardItem>(), cancellationToken);

    internal static ClipboardReceipt Clear(ClipboardPolicy policy, string expectedGeneration, IClipboardApi api, CancellationToken cancellationToken = default) =>
        Write(policy, expectedGeneration, Array.Empty<ClipboardItem>(), api, cancellationToken);

    /// <summary>
    /// Opens the clipboard, retrying while another process holds it. Retrying out is busy, or
    /// unavailable when the window station has no interactive desktop and so no clipboard. The
    /// bound is judged before each attempt, so a timeout or cancel ends the retries.
    /// </summary>
    private static void OpenClipboard(IClipboardApi api, ClipboardBound bound)
    {
        for (var attempt = 0; attempt < OpenAttempts; attempt++)
        {
            bound.Check();
            if (api.Open())
            {
                return;
            }
            api.Sleep(10);
        }
        if (api.Interactive() == false)
        {
            throw new ClipboardException(ClipboardErrorKind.Unavailable, "this session has no clipboard");
        }
        throw new ClipboardException(ClipboardErrorKind.Busy, "the clipboard is held by another process");
    }

    /// <summary>A failed Win32 call: io, with the thread's Win32 error as the inner exception.</summary>
    private static ClipboardException Native(IClipboardApi api, string message) =>
        new(ClipboardErrorKind.Io, message, new Win32Exception(api.LastError()));

    private static string FormatName(IClipboardApi api, uint identifier)
    {
        if (StandardNames.TryGetValue(identifier, out var name))
        {
            return name;
        }
        return api.RegisteredName(identifier) ?? $"CF_{identifier}";
    }

    private static uint FormatIdentifier(IClipboardApi api, string name)
    {
        if (StandardFormats.TryGetValue(name, out var identifier))
        {
            return identifier;
        }
        identifier = api.Register(name);
        if (identifier == 0)
        {
            throw Native(api, "cannot register a clipboard format");
        }
        return identifier;
    }

    private static byte[] ReadGlobal(IClipboardApi api, IntPtr handle)
    {
        var size = api.Size(handle);
        if (size <= 0)
        {
            throw new ClipboardException(ClipboardErrorKind.Invalid, "clipboard memory has no size");
        }
        if (size > int.MaxValue)
        {
            throw new ClipboardException(ClipboardErrorKind.Limit, "clipboard bytes exceed the policy");
        }
        var pointer = api.Lock(handle);
        if (pointer == IntPtr.Zero)
        {
            throw Native(api, "cannot lock clipboard memory");
        }
        try
        {
            var data = new byte[(int)size];
            Marshal.Copy(pointer, data, 0, data.Length);
            return data;
        }
        finally
        {
            api.Unlock(handle);
        }
    }

    private static void WriteGlobal(IClipboardApi api, uint identifier, byte[] data)
    {
        if (data.Length == 0)
        {
            // Invalid, as Go's setGlobalClipboardData: Windows has no empty global memory object.
            throw new ClipboardException(ClipboardErrorKind.Invalid, "clipboard representation is empty");
        }
        var handle = api.Alloc(data.Length);
        if (handle == IntPtr.Zero)
        {
            throw Native(api, "cannot allocate clipboard memory");
        }
        try
        {
            var pointer = api.Lock(handle);
            if (pointer == IntPtr.Zero)
            {
                throw Native(api, "cannot lock clipboard memory");
            }
            try
            {
                Marshal.Copy(data, 0, pointer, data.Length);
            }
            finally
            {
                api.Unlock(handle);
            }
            if (api.SetData(identifier, handle) == IntPtr.Zero)
            {
                throw Native(api, "cannot set clipboard data");
            }
            // Ownership transfers to the system on success.
            handle = IntPtr.Zero;
        }
        finally
        {
            if (handle != IntPtr.Zero)
            {
                api.Free(handle);
            }
        }
    }
}

internal sealed class Win32ClipboardApi : IClipboardApi
{
    internal static readonly Win32ClipboardApi Instance = new();

    private const uint GlobalMoveable = 0x0002;
    private const int UoiFlags = 1;
    private const int WsfVisible = 0x0001;

    public bool Open() => OpenClipboard(IntPtr.Zero);
    public void Close() => CloseClipboard();
    public bool Empty() => EmptyClipboard();
    public uint EnumFormats(uint format) => EnumClipboardFormats(format);
    public IntPtr GetData(uint format) => GetClipboardData(format);
    public IntPtr SetData(uint format, IntPtr memory) => SetClipboardData(format, memory);
    public uint Register(string name) => RegisterClipboardFormat(name);

    public string? RegisteredName(uint format)
    {
        var buffer = new StringBuilder(256);
        var written = GetClipboardFormatName(format, buffer, buffer.Capacity);
        return written > 0 ? buffer.ToString(0, written) : null;
    }

    public uint Sequence() => GetClipboardSequenceNumber();
    public IntPtr Alloc(int bytes) => GlobalAlloc(GlobalMoveable, (UIntPtr)bytes);
    public void Free(IntPtr memory) => GlobalFree(memory);
    public IntPtr Lock(IntPtr memory) => GlobalLock(memory);
    public void Unlock(IntPtr memory) => GlobalUnlock(memory);
    public long Size(IntPtr memory) => (long)(ulong)GlobalSize(memory);
    public int LastError() => Marshal.GetLastPInvokeError();
    public void Sleep(int milliseconds) => Thread.Sleep(milliseconds);

    public bool? Interactive()
    {
        var station = GetProcessWindowStation();
        if (station == IntPtr.Zero)
        {
            return null;
        }
        var flags = new UserObjectFlags();
        if (!GetUserObjectInformation(station, UoiFlags, ref flags, Marshal.SizeOf<UserObjectFlags>(), out _))
        {
            return null;
        }
        return (flags.Flags & WsfVisible) != 0;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct UserObjectFlags
    {
        public int Inherit;
        public int Reserved;
        public int Flags;
    }

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool OpenClipboard(IntPtr owner);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool CloseClipboard();

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool EmptyClipboard();

    [DllImport("user32.dll", SetLastError = true)]
    private static extern uint EnumClipboardFormats(uint format);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern IntPtr GetClipboardData(uint format);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern IntPtr SetClipboardData(uint format, IntPtr memory);

    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    private static extern uint RegisterClipboardFormat(string format);

    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    private static extern int GetClipboardFormatName(uint format, StringBuilder name, int length);

    [DllImport("user32.dll")]
    private static extern uint GetClipboardSequenceNumber();

    [DllImport("user32.dll", SetLastError = true)]
    private static extern IntPtr GetProcessWindowStation();

    [DllImport("user32.dll", EntryPoint = "GetUserObjectInformationW", SetLastError = true)]
    private static extern bool GetUserObjectInformation(IntPtr handle, int index, ref UserObjectFlags info, int length, out int needed);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr GlobalAlloc(uint flags, UIntPtr bytes);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr GlobalFree(IntPtr memory);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr GlobalLock(IntPtr memory);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GlobalUnlock(IntPtr memory);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern UIntPtr GlobalSize(IntPtr memory);
}
