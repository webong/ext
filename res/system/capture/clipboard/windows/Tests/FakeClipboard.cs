// A clipboard in process memory behind the library's IClipboardApi seam, so each Win32 failure and
// its code can be tested without a clipboard. Each Fail* flag makes that one call fail.
using System.Runtime.InteropServices;
using Webong.Ext.System.Windows;

sealed class FakeClipboard : IClipboardApi
{
    public const int Error = 1418; // ERROR_CLIPBOARD_NOT_OPEN, any Win32 error will do
    readonly SortedDictionary<uint, IntPtr> data = new();
    readonly Dictionary<IntPtr, int> sizes = new();
    readonly Dictionary<string, uint> registered = new(StringComparer.Ordinal);
    uint sequence = 1;

    public bool Held, FailEmpty, FailGetData, FailSetData, FailRegister, FailAlloc, FailLock;
    public bool? InteractiveStation = true;
    public int Opens, Closes;

    public bool Open()
    {
        Opens++;
        return !Held;
    }

    public void Close() => Closes++;

    public bool Empty()
    {
        if (FailEmpty) return false;
        data.Clear();
        sequence++;
        return true;
    }

    public uint EnumFormats(uint format) => data.Keys.FirstOrDefault(k => k > format);
    public IntPtr GetData(uint format) => FailGetData ? IntPtr.Zero : data.GetValueOrDefault(format);

    public IntPtr SetData(uint format, IntPtr memory)
    {
        if (FailSetData) return IntPtr.Zero;
        data[format] = memory;
        sequence++;
        return memory;
    }

    public uint Register(string name)
    {
        if (FailRegister) return 0;
        if (!registered.TryGetValue(name, out var id)) registered[name] = id = 0xC000u + (uint)registered.Count;
        return id;
    }

    public string? RegisteredName(uint format) => registered.FirstOrDefault(r => r.Value == format).Key;
    public uint Sequence() => sequence;

    public IntPtr Alloc(int bytes)
    {
        if (FailAlloc) return IntPtr.Zero;
        var memory = Marshal.AllocHGlobal(bytes);
        sizes[memory] = bytes;
        return memory;
    }

    public void Free(IntPtr memory)
    {
        sizes.Remove(memory);
        Marshal.FreeHGlobal(memory);
    }

    public IntPtr Lock(IntPtr memory) => FailLock ? IntPtr.Zero : memory;
    public void Unlock(IntPtr memory) { }
    public long Size(IntPtr memory) => sizes.GetValueOrDefault(memory);
    public int LastError() => Error;
    public bool? Interactive() => InteractiveStation;
    public void Sleep(int milliseconds) { }
    public int Allocated => sizes.Count;
}
