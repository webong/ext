using System.Runtime.InteropServices;
using System.Text;

namespace Webong.Ext.System.Windows;

/// <summary>
/// Bounds and permissions fixed by the embedding application before native
/// clipboard access. Formats are Win32 names: the four standard names this
/// library understands, or a registered format name.
/// </summary>
public sealed record ClipboardPolicy(
    IReadOnlyCollection<string> AllowedFormats,
    int MaxBytes = 16 * 1024 * 1024,
    int MaxItems = 64,
    int MaxFormats = 32,
    bool AllowRead = false,
    bool AllowWrite = false)
{
    public ClipboardPolicy Validate()
    {
        if (MaxBytes <= 0 || MaxBytes > 16 * 1024 * 1024 ||
            MaxItems < 1 || MaxItems > 64 ||
            MaxFormats < 1 || MaxFormats > 32 ||
            AllowedFormats is null || AllowedFormats.Count < 1 || AllowedFormats.Count > MaxFormats)
        {
            throw new ClipboardException("invalid", "clipboard needs bounded formats and byte/item ceilings");
        }
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var format in AllowedFormats)
        {
            if (string.IsNullOrEmpty(format) || format.Length > 256 || !seen.Add(format))
            {
                throw new ClipboardException("invalid", "clipboard formats must be unique names of at most 256 characters");
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
}

public sealed class ClipboardException : Exception
{
    public ClipboardErrorKind Kind { get; }

    public ClipboardException(ClipboardErrorKind kind, string message, Exception? inner = null)
        : base(message, inner)
    {
        Kind = kind;
    }

    public ClipboardException(string kind, string message, Exception? inner = null)
        : this(kind switch
        {
            "denied" => ClipboardErrorKind.Denied,
            "limit" => ClipboardErrorKind.Limit,
            "changed" => ClipboardErrorKind.Changed,
            _ => ClipboardErrorKind.Invalid,
        }, message, inner)
    {
    }
}

public sealed record ClipboardRepresentation(string Format, byte[] Data);

public sealed record ClipboardItem(IReadOnlyList<ClipboardRepresentation> Representations);

public sealed record ClipboardSnapshot(string Generation, IReadOnlyList<ClipboardItem> Items);

public sealed record ClipboardReceipt(string Generation, string Dispatch);

/// <summary>
/// Bounded Win32 clipboard access for local consumers. It holds no history,
/// prompts for nothing, and retains no remote protocol or approval state.
/// Windows exposes one clipboard item with several formats, so a write with
/// more than one item fails instead of being flattened.
/// </summary>
public static class SystemClipboard
{
    private const uint CFText = 1;
    private const uint CFDib = 8;
    private const uint CFUnicodeText = 13;
    private const uint CFDibV5 = 17;
    private const uint GlobalMoveable = 0x0002;

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

    public static string Generation() => $"windows:{GetClipboardSequenceNumber()}";

    public static ClipboardSnapshot Read(ClipboardPolicy policy)
    {
        policy.Validate();
        if (!policy.AllowRead)
        {
            throw new ClipboardException(ClipboardErrorKind.Denied, "clipboard read is not allowed");
        }
        OpenClipboard();
        try
        {
            var before = Generation();
            var allowed = new HashSet<string>(policy.AllowedFormats, StringComparer.Ordinal);
            var representations = new List<ClipboardRepresentation>();
            var total = 0L;
            uint identifier = 0;
            while (true)
            {
                identifier = EnumClipboardFormats(identifier);
                if (identifier == 0)
                {
                    break;
                }
                var name = FormatName(identifier);
                if (!allowed.Contains(name))
                {
                    continue;
                }
                var handle = GetClipboardData(identifier);
                if (handle == IntPtr.Zero)
                {
                    throw new ClipboardException(ClipboardErrorKind.Denied,
                        $"cannot read clipboard format {name}");
                }
                var data = ReadGlobal(handle);
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
                throw new ClipboardException(ClipboardErrorKind.Denied,
                    "clipboard contains none of the allowed formats");
            }
            if (Generation() != before)
            {
                throw new ClipboardException(ClipboardErrorKind.Changed,
                    "clipboard changed while it was read");
            }
            var item = new ClipboardItem(representations);
            return new ClipboardSnapshot(before, new[] { item });
        }
        finally
        {
            CloseClipboard();
        }
    }

    public static ClipboardReceipt Write(
        ClipboardPolicy policy,
        string expectedGeneration,
        IReadOnlyList<ClipboardItem> items)
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
        if (items.Count > policy.MaxItems)
        {
            throw new ClipboardException(ClipboardErrorKind.Limit, "clipboard items exceed the policy");
        }
        if (Generation() != expectedGeneration)
        {
            throw new ClipboardException(ClipboardErrorKind.Changed,
                "clipboard generation changed");
        }
        var allowed = new HashSet<string>(policy.AllowedFormats, StringComparer.Ordinal);
        if (items.Count == 1)
        {
            var item = items[0];
            if (item.Representations.Count < 1 || item.Representations.Count > policy.MaxFormats)
            {
                throw new ClipboardException(ClipboardErrorKind.Invalid,
                    "clipboard item needs between one and the format ceiling");
            }
            var seen = new HashSet<string>(StringComparer.Ordinal);
            var total = 0L;
            foreach (var representation in item.Representations)
            {
                if (!allowed.Contains(representation.Format) || !seen.Add(representation.Format))
                {
                    throw new ClipboardException(ClipboardErrorKind.Denied,
                        "clipboard format is not allowed or is repeated");
                }
                total += representation.Data.Length;
                if (total > policy.MaxBytes)
                {
                    throw new ClipboardException(ClipboardErrorKind.Limit,
                        "clipboard bytes exceed the policy");
                }
            }
        }
        OpenClipboard();
        try
        {
            if (Generation() != expectedGeneration)
            {
                throw new ClipboardException(ClipboardErrorKind.Changed,
                    "clipboard generation changed before the write");
            }
            if (!EmptyClipboard())
            {
                throw new ClipboardException(ClipboardErrorKind.Denied, "cannot clear the clipboard");
            }
            if (items.Count == 1)
            {
                foreach (var representation in items[0].Representations)
                {
                    var identifier = FormatIdentifier(representation.Format);
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
                    WriteGlobal(identifier, data);
                }
            }
            return new ClipboardReceipt(Generation(), "dispatched");
        }
        finally
        {
            CloseClipboard();
        }
    }

    public static ClipboardReceipt Clear(ClipboardPolicy policy, string expectedGeneration) =>
        Write(policy, expectedGeneration, Array.Empty<ClipboardItem>());

    private static void OpenClipboard()
    {
        for (var attempt = 0; attempt < 50; attempt++)
        {
            if (NativeOpenClipboard(IntPtr.Zero))
            {
                return;
            }
            Thread.Sleep(10);
        }
        throw new ClipboardException(ClipboardErrorKind.Denied,
            "the clipboard is held by another process");
    }

    private static string FormatName(uint identifier)
    {
        if (StandardNames.TryGetValue(identifier, out var name))
        {
            return name;
        }
        var buffer = new StringBuilder(256);
        var written = GetClipboardFormatName(identifier, buffer, buffer.Capacity);
        return written > 0 ? buffer.ToString(0, written) : $"CF_{identifier}";
    }

    private static uint FormatIdentifier(string name)
    {
        if (StandardFormats.TryGetValue(name, out var identifier))
        {
            return identifier;
        }
        identifier = RegisterClipboardFormat(name);
        if (identifier == 0)
        {
            throw new ClipboardException(ClipboardErrorKind.Denied,
                $"cannot register clipboard format {name}");
        }
        return identifier;
    }

    private static byte[] ReadGlobal(IntPtr handle)
    {
        var size = GlobalSize(handle);
        if (size == UIntPtr.Zero)
        {
            throw new ClipboardException(ClipboardErrorKind.Invalid, "clipboard memory has no size");
        }
        var pointer = GlobalLock(handle);
        if (pointer == IntPtr.Zero)
        {
            throw new ClipboardException(ClipboardErrorKind.Denied, "cannot lock clipboard memory");
        }
        try
        {
            var data = new byte[(int)size];
            Marshal.Copy(pointer, data, 0, data.Length);
            return data;
        }
        finally
        {
            GlobalUnlock(handle);
        }
    }

    private static void WriteGlobal(uint identifier, byte[] data)
    {
        if (data.Length == 0)
        {
            throw new ClipboardException(ClipboardErrorKind.Invalid, "clipboard representation is empty");
        }
        var handle = GlobalAlloc(GlobalMoveable, (UIntPtr)data.Length);
        if (handle == IntPtr.Zero)
        {
            throw new ClipboardException(ClipboardErrorKind.Denied, "cannot allocate clipboard memory");
        }
        try
        {
            var pointer = GlobalLock(handle);
            if (pointer == IntPtr.Zero)
            {
                throw new ClipboardException(ClipboardErrorKind.Denied, "cannot lock clipboard memory");
            }
            try
            {
                Marshal.Copy(data, 0, pointer, data.Length);
            }
            finally
            {
                GlobalUnlock(handle);
            }
            if (SetClipboardData(identifier, handle) == IntPtr.Zero)
            {
                throw new ClipboardException(ClipboardErrorKind.Denied, "cannot set clipboard data");
            }
            // Ownership transfers to the system on success.
            handle = IntPtr.Zero;
        }
        finally
        {
            if (handle != IntPtr.Zero)
            {
                GlobalFree(handle);
            }
        }
    }

    [DllImport("user32.dll", EntryPoint = "OpenClipboard", SetLastError = true)]
    private static extern bool NativeOpenClipboard(IntPtr owner);

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
