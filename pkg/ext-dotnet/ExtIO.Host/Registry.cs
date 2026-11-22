using System.Text;
using System.Text.Json.Nodes;

namespace ExtIO.Host;

/// <summary>
/// One reverse request a plugin made, for the hook that serves it: <see cref="Request"/> is the
/// Request (apiVersion, id, plugin, contract, operation, surface, deadline, payload and, for a
/// call-scoped request, parent), <see cref="Remaining"/> the time left to it. Valid only while the
/// hook runs.
/// </summary>
public sealed unsafe class ReverseCall
{
    private readonly NativeApi api;
    private readonly nint cancel;

    internal ReverseCall(NativeApi api, ExtCallOptions* options, byte* json, nuint len)
    {
        this.api = api;
        cancel = (nint)options->Cancel;
        Remaining = TimeSpan.FromMilliseconds(options->TimeoutMs);
        Request = JsonNode.Parse(new ReadOnlySpan<byte>(json, checked((int)len)));
    }

    public JsonNode? Request { get; }
    public TimeSpan Remaining { get; }

    /// <summary>True once the call it belongs to ended, the caller canceled it or the host closed.</summary>
    public bool Canceled => cancel != 0 && api.CancelIsSignaled(cancel) != 0;
}

/// <summary>Decides one reverse request: true allows.</summary>
public delegate bool ReverseAuthorize(ReverseCall call);

/// <summary>Serves one reverse request: a payload (null is null), or a <see cref="RemoteException"/> the plugin receives as it is.</summary>
public delegate JsonNode? ReverseHandle(ReverseCall call);

/// <summary>
/// What a host offers its plugin (<see cref="Host.Serve"/>): the operations its descriptor lists
/// under reverse. The engine admits or refuses each request with a stable code; Authorize and
/// Handle are the host's policy. Zero bounds use the defaults (30 s, 1 MiB, 4).
/// </summary>
public sealed record ReverseServices(ReverseAuthorize Authorize, ReverseHandle? Handle)
{
    public TimeSpan Timeout { get; init; }
    public uint MaxPayloadBytes { get; init; }
    public uint MaxInFlight { get; init; }
}

/// <summary>One end of a registry edge: a contract and an operation in it.</summary>
public readonly record struct RegistryEnd(string Name, string Version, string Operation);

/// <summary>
/// A guest registry (ext_registry_* in ext_plugin_host.h): a reverse call one guest makes is served by a
/// forward call to another guest, along an edge declared with <see cref="Route"/>. An undeclared
/// edge is refused with reverse_undeclared, an unregistered target with reverse_unavailable, and a
/// call that would re-enter a host already on its call chain with denied; the target's answer
/// passes through unchanged. A host is a source once <see cref="Host.Attach"/> names the registry,
/// and a target once <see cref="Register"/> names a contract it serves; it leaves the registry when
/// it is disposed. The native registry is counted: this object and every attached or registered
/// host hold a reference, so it is freed only when none can call into it. Safe to use from several
/// threads; failures throw <see cref="HostException"/>.
/// </summary>
public sealed unsafe class Registry
{
    internal readonly NativeApi Api;
    internal readonly nint Handle;
    private readonly Dictionary<(string, string), WeakReference<Host>> targets = new();
    private int refs = 1;

    public Registry(Engine engine)
    {
        Api = engine.Api;
        nint created;
        var status = Api.RegistryCreate(&created);
        if (status != 0)
        {
            GC.SuppressFinalize(this);
            throw new HostException(status, "registry");
        }
        Handle = created;
    }

    ~Registry() => Release();

    internal void Retain() => Interlocked.Increment(ref refs);

    internal void Release()
    {
        if (Interlocked.Decrement(ref refs) == 0)
        {
            Api.RegistryDestroy(Handle);
        }
    }

    internal static byte[] Z(string s) => Encoding.UTF8.GetBytes(s + "\0");

    /// <summary>Names <paramref name="host"/>, started and listing the contract, as the one serving it.</summary>
    public void Register(string name, string version, Host host)
    {
        host.RegisterIn(this, name, version);
        lock (targets)
        {
            targets[(name, version)] = new WeakReference<Host>(host);
        }
    }

    internal int RegisterRaw(string name, string version, nint host)
    {
        fixed (byte* n = Z(name), v = Z(version))
        {
            return Api.RegistryRegister(Handle, n, v, host);
        }
    }

    internal int UnregisterRaw(string name, string version)
    {
        fixed (byte* n = Z(name), v = Z(version))
        {
            return Api.RegistryUnregister(Handle, n, v);
        }
    }

    internal void Forget(string name, string version)
    {
        lock (targets)
        {
            targets.Remove((name, version));
        }
    }

    /// <summary>Removes the contract and waits for the forward calls routed to its host.</summary>
    public void Unregister(string name, string version)
    {
        var status = UnregisterRaw(name, version);
        if (status != 0)
        {
            throw new HostException(status, "unregister");
        }
        WeakReference<Host>? host;
        lock (targets)
        {
            targets.Remove((name, version), out host);
        }
        if (host is not null && host.TryGetTarget(out var target))
        {
            target.Forget(this, name, version);
        }
    }

    /// <summary>A reverse call of <paramref name="from"/> is served by a call of <paramref name="to"/>.</summary>
    public void Route(RegistryEnd from, RegistryEnd to)
    {
        fixed (byte* fn = Z(from.Name), fv = Z(from.Version), fo = Z(from.Operation),
               tn = Z(to.Name), tv = Z(to.Version), to_ = Z(to.Operation))
        {
            var edge = new ExtRegistryEdge
            {
                StructSize = (uint)sizeof(ExtRegistryEdge),
                FromName = fn, FromVersion = fv, FromOperation = fo,
                ToName = tn, ToVersion = tv, ToOperation = to_,
            };
            var status = Api.RegistryRoute(Handle, &edge);
            if (status != 0)
            {
                throw new HostException(status, "route");
            }
        }
    }

    internal static byte[] Encode(JsonNode? value) =>
        value is null ? "null"u8.ToArray() : Encoding.UTF8.GetBytes(value.ToJsonString());

    internal static byte[] EncodeError(RemoteException e)
    {
        var error = new JsonObject { ["code"] = e.ErrorCode, ["message"] = e.Message };
        if (e.RetryAfterMilliseconds is long retry && retry != 0)
        {
            error["retryAfterMilliseconds"] = retry;
        }
        return Encoding.UTF8.GetBytes(error.ToJsonString());
    }
}
