using System.Text;
using System.Text.Json.Nodes;

namespace ExtIO.Host;

/// <summary>An owned native package writer lock. Use a using scope for deterministic release.</summary>
public sealed unsafe class PackageWriter : IDisposable
{
    private readonly NativeApi api;
    private readonly object gate = new();
    private ulong handle;

    internal PackageWriter(NativeApi api, string root)
    {
        this.api = api;
        ArgumentNullException.ThrowIfNull(root);
        if (api.ObjectOpen == null || api.ObjectClose == null)
            throw new HostException(4, "package writer");
        var config = Encoding.UTF8.GetBytes(new JsonObject
        {
            ["object"] = "package_writer", ["root"] = root,
        }.ToJsonString());
        ulong opened = 0;
        fixed (byte* bytes = config)
        {
            var status = api.ObjectOpen(bytes, (nuint)config.Length, &opened);
            if (status != 0) throw new HostException(status, "package writer open");
        }
        handle = opened;
    }

    /// <summary>Releases once; reports ownership changes without deleting another writer's lock.</summary>
    public void Dispose()
    {
        var status = Release();
        GC.SuppressFinalize(this);
        if (status != 0) throw new HostException(status, "package writer close");
    }

    private int Release()
    {
        lock (gate)
        {
            if (handle == 0) return 0;
            var owned = handle;
            handle = 0; // Native close consumes the handle even if release reports a mismatch.
            return api.ObjectClose(owned);
        }
    }

    ~PackageWriter() => Release();
}
