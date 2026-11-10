using System.Runtime.CompilerServices;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace ExtIO.Host;

/// <summary>A host failure: the engine's status by name (<see cref="Status"/>: "denied", "closed" ...).</summary>
public sealed class HostException : Exception
{
    internal static readonly string[] StatusNames =
    [
        "ok", "invalid", "denied", "mismatch", "unsupported", "closed", "timeout", "io", "nomem",
        "draining", "not_found", "ambiguous", "canceled", "updating", "capacity", "sequence",
        "protocol", "expired", "exists", "missing",
    ];

    public HostException(int code, string action)
        : base($"ext host: {(action.Length > 0 ? action + ": " : "")}{NameOf(code)}")
    {
        Code = code;
        Status = NameOf(code);
        Action = action;
    }

    /// <summary>The engine's status number.</summary>
    public int Code { get; }

    /// <summary>The engine's status by name.</summary>
    public string Status { get; }

    /// <summary>What was being done.</summary>
    public string Action { get; }

    private static string NameOf(int code) => code >= 0 && code < StatusNames.Length ? StatusNames[code] : "unknown";
}

/// <summary>A domain error the plugin returned: its public code, message and retry hint.</summary>
public sealed class RemoteException(string code, string message, long? retryAfterMilliseconds) : Exception(message)
{
    public string ErrorCode { get; } = code;
    public long? RetryAfterMilliseconds { get; } = retryAfterMilliseconds;
}

/// <summary>A policy: receives the decoded JSON (a descriptor or a request) and returns true to allow.</summary>
public delegate bool Policy(JsonNode? json);

/// <summary>
/// The C engine (lib/plugin), loaded from a path, <c>EXT_HOST_LIBRARY</c>, or <c>libext_host</c>
/// on the loader path. The engine does the work a host must get right: admission, the handshake
/// check against the descriptor, request and response validation, deadlines and the session's
/// lifecycle; this binding only moves bytes and policy decisions. An engine is never unloaded.
/// </summary>
public sealed unsafe class Engine
{
    internal readonly NativeApi Api;

    /// <summary>Loads the engine; throws <see cref="DllNotFoundException"/> when none loads or its ABI is not 2.</summary>
    public Engine(string? path = null)
    {
        path ??= System.Environment.GetEnvironmentVariable("EXT_HOST_LIBRARY");
        string[] candidates = !string.IsNullOrEmpty(path)
            ? [path]
            : OperatingSystem.IsWindows() ? ["ext_host.dll"]
            : OperatingSystem.IsMacOS() ? ["libext_host.dylib"]
            : ["libext_host.so"];
        Exception? last = null;
        foreach (var candidate in candidates)
        {
            if (NativeLibrary.TryLoad(candidate, out var library))
            {
                Api = new NativeApi(library, candidate);
                return;
            }
            last = new DllNotFoundException($"{candidate} could not be loaded");
        }
        throw new DllNotFoundException(
            "the ext engine library was not found: set EXT_HOST_LIBRARY to libext_host, or pass its path", last);
    }

    /// <summary>
    /// A host for a JSON-line guest started as <paramref name="executable"/> (an absolute path, no
    /// shell). <paramref name="environment"/> entries are NAME=value, added to a scrubbed base of
    /// PATH; the child's stderr is kept up to <paramref name="stderrBytes"/>. <paramref name="verify"/>
    /// sees the descriptor before the child starts and <paramref name="authorize"/> each validated
    /// request; null allows, and an exception denies. Neither may block or use this host.
    /// </summary>
    public Host Process(
        JsonNode descriptor,
        string executable,
        IReadOnlyList<string>? arguments = null,
        IReadOnlyList<string>? environment = null,
        string? workingDirectory = null,
        uint stderrBytes = 4096,
        Policy? verify = null,
        Policy? authorize = null)
    {
        using var strings = new Utf8Strings();
        var launch = new ExtProcessLaunch
        {
            StructSize = (uint)sizeof(ExtProcessLaunch),
            Executable = strings.Add(executable),
            WorkingDirectory = workingDirectory is null ? null : strings.Add(workingDirectory),
            StderrBytes = stderrBytes,
        };
        launch.Arguments = strings.AddArray(arguments, out launch.ArgumentCount);
        launch.Environment = strings.AddArray(environment, out launch.EnvironmentCount);
        var backend = new ExtBackendOptions
        {
            StructSize = (uint)sizeof(ExtBackendOptions),
            Kind = NativeApi.BackendProcessLaunch,
            Config = &launch,
            ConfigSize = (nuint)sizeof(ExtProcessLaunch),
        };
        return new Host(this, Encode(descriptor), &backend, verify, authorize, null);
    }

    /// <summary>
    /// A host for a guest library that exports the C ABI of ext_plugin.h. <paramref name="path"/>
    /// is an absolute path to a verified, revision-specific artifact; it is loaded when the host
    /// starts, after <paramref name="verify"/>, and is never unloaded. Its native code runs in this
    /// process with its full access.
    /// </summary>
    public Host SharedLibrary(JsonNode descriptor, string path, Policy? verify = null, Policy? authorize = null)
    {
        using var strings = new Utf8Strings();
        var options = new ExtSharedLibraryOptions { StructSize = (uint)sizeof(ExtSharedLibraryOptions), Path = strings.Add(path) };
        var extension = default(ExtExtensionContext);
        var status = Api.SharedLibrary(&options, &extension);
        if (status != 0)
        {
            throw new HostException(status, "shared library");
        }
        var backend = new ExtBackendOptions
        {
            StructSize = (uint)sizeof(ExtBackendOptions),
            Kind = NativeApi.BackendExtensionContext,
            Config = &extension,
            ConfigSize = (nuint)sizeof(ExtExtensionContext),
        };
        return new Host(this, Encode(descriptor), &backend, verify, authorize, &extension);
    }

    private static byte[] Encode(JsonNode descriptor) => Encoding.UTF8.GetBytes(descriptor.ToJsonString());

    /// <summary>NUL-terminated UTF-8 copies in native memory, freed together.</summary>
    private sealed class Utf8Strings : IDisposable
    {
        private readonly List<nint> blocks = [];

        public byte* Add(string s)
        {
            if (s.Contains('\0'))
            {
                throw new ArgumentException("a string passed to the engine may not contain NUL");
            }
            var bytes = Encoding.UTF8.GetBytes(s);
            var block = (byte*)NativeMemory.Alloc((nuint)bytes.Length + 1);
            blocks.Add((nint)block);
            bytes.CopyTo(new Span<byte>(block, bytes.Length));
            block[bytes.Length] = 0;
            return block;
        }

        public byte** AddArray(IReadOnlyList<string>? items, out nuint count)
        {
            count = 0;
            if (items is null || items.Count == 0)
            {
                return null;
            }
            var array = (byte**)NativeMemory.Alloc((nuint)(items.Count * sizeof(byte*)));
            blocks.Add((nint)array);
            for (var i = 0; i < items.Count; i++)
            {
                array[i] = Add(items[i]);
            }
            count = (nuint)items.Count;
            return array;
        }

        public void Dispose()
        {
            foreach (var block in blocks)
            {
                NativeMemory.Free((void*)block);
            }
        }
    }
}

/// <summary>
/// One session with one plugin. Use <see cref="Engine.Process"/> or <see cref="Engine.SharedLibrary"/>.
/// Calls may come from several threads; the engine serializes them for a process or a shared
/// library. A <see cref="CancellationToken"/> cancels a call in progress. <see cref="Close"/>
/// aborts; <see cref="Dispose"/> closes, waits for the calls in flight to return and frees the host.
/// </summary>
public sealed unsafe class Host : IDisposable
{
    private static readonly string[] States = ["created", "ready", "draining", "closed", "failed"];
    private const int StderrCapacity = 65536;

    [ThreadStatic] private static Host? inPolicy;

    private readonly NativeApi api;
    private readonly Policy? verify;
    private readonly Policy? authorize;
    private readonly object gate = new();
    private GCHandle self;
    private nint handle;
    private int active;
    private bool disposing;

    internal Host(Engine engine, byte[] descriptor, ExtBackendOptions* backend, Policy? verify, Policy? authorize, ExtExtensionContext* extension)
    {
        api = engine.Api;
        this.verify = verify;
        this.authorize = authorize;
        self = GCHandle.Alloc(this, GCHandleType.Weak);
        fixed (byte* data = descriptor)
        {
            var options = new ExtHostOptions
            {
                AbiVersion = NativeApi.AbiVersion,
                StructSize = (uint)sizeof(ExtHostOptions),
                Descriptor = data,
                DescriptorLen = (nuint)descriptor.Length,
                Verify = &VerifyCallback,
                Authorize = &AuthorizeCallback,
                User = (void*)GCHandle.ToIntPtr(self),
            };
            nint created;
            var status = api.Create(&options, backend, &created);
            if (status != 0)
            {
                self.Free();
                if (extension is not null)
                {
                    extension->Release(extension->User); // ownership stays with the caller on a failed create
                }
                GC.SuppressFinalize(this);
                throw new HostException(status, "create");
            }
            handle = created;
        }
    }

    ~Host()
    {
        // Unreachable, so no call is in flight.
        if (handle != 0)
        {
            api.Close(handle);
            api.Destroy(handle);
            handle = 0;
        }
        if (self.IsAllocated)
        {
            self.Free();
        }
    }

    [UnmanagedCallersOnly]
    private static int VerifyCallback(void* user, byte* json, nuint len) => RunPolicy(user, json, len, verify: true);

    [UnmanagedCallersOnly]
    private static int AuthorizeCallback(void* user, byte* json, nuint len) => RunPolicy(user, json, len, verify: false);

    /// <summary>Runs on the thread making the call. Allows only on true; never lets an exception reach the engine.</summary>
    private static int RunPolicy(void* user, byte* json, nuint len, bool verify)
    {
        try
        {
            if (GCHandle.FromIntPtr((nint)user).Target is not Host host)
            {
                return 1;
            }
            var policy = verify ? host.verify : host.authorize;
            if (policy is null)
            {
                return 0;
            }
            var previous = inPolicy;
            inPolicy = host;
            try
            {
                return policy(JsonNode.Parse(new ReadOnlySpan<byte>(json, checked((int)len)))) ? 0 : 1;
            }
            finally
            {
                inPolicy = previous;
            }
        }
        catch
        {
            return 1;
        }
    }

    /// <summary>Admits one call from this thread, or throws: destroyed, or from inside one of this host's policies.</summary>
    private nint Enter(string action)
    {
        if (ReferenceEquals(inPolicy, this))
        {
            throw new HostException(1, action); // a policy may not reenter its host
        }
        lock (gate)
        {
            if (handle == 0 || disposing)
            {
                throw new HostException(5, "destroyed");
            }
            active++;
            return handle;
        }
    }

    private void Leave()
    {
        lock (gate)
        {
            active--;
            Monitor.PulseAll(gate);
        }
    }

    private static ExtCallOptions Options(TimeSpan? timeout)
    {
        var ms = (timeout ?? TimeSpan.FromSeconds(3)).TotalMilliseconds;
        if (!(ms >= 1))
        {
            throw new ArgumentOutOfRangeException(nameof(timeout), "timeout must be at least 1 ms");
        }
        return new ExtCallOptions { StructSize = (uint)sizeof(ExtCallOptions), TimeoutMs = ms >= uint.MaxValue ? uint.MaxValue : (uint)ms };
    }

    /// <summary>Runs one engine call with a cancel that <paramref name="token"/> signals.</summary>
    private int Run(string action, TimeSpan? timeout, CancellationToken token, Func<nint, nint, int> body)
    {
        var h = Enter(action);
        try
        {
            nint cancel;
            var status = api.CancelCreate(&cancel);
            if (status != 0)
            {
                throw new HostException(status, action);
            }
            try
            {
                using (token.Register(static state => ((Host)((object[])state!)[0]).api.CancelSignal((nint)((object[])state!)[1]),
                           new object[] { this, cancel }))
                {
                    return body(h, cancel);
                }
            }
            finally
            {
                api.CancelDestroy(cancel); // the call and the registration have both ended
            }
        }
        finally
        {
            Leave();
        }
    }

    /// <summary>Starts the guest and checks its handshake against the descriptor.</summary>
    public void Start(TimeSpan? timeout = null, CancellationToken token = default)
    {
        var options = Options(timeout);
        var status = Run("start", timeout, token, (h, cancel) =>
        {
            var o = options;
            o.Cancel = (void*)cancel;
            return api.Start(h, &o);
        });
        if (status != 0)
        {
            throw new HostException(status, "start");
        }
    }

    private byte[] Exchange(bool call, ReadOnlySpan<byte> data, TimeSpan? timeout, CancellationToken token, string action)
    {
        var options = Options(timeout);
        var copy = data.ToArray();
        byte[]? response = null;
        var status = Run(action, timeout, token, (h, cancel) =>
        {
            var o = options;
            o.Cancel = (void*)cancel;
            var output = default(ExtBuffer);
            int s;
            fixed (byte* input = copy)
            {
                s = call
                    ? api.Call(h, input, (nuint)copy.Length, &o, &output)
                    : api.Invoke(h, input, (nuint)copy.Length, &o, &output);
            }
            if (output.Data is not null)
            {
                response = new ReadOnlySpan<byte>(output.Data, checked((int)output.Len)).ToArray();
                api.BufferFree(&output);
            }
            return s;
        });
        if (status != 0)
        {
            throw new HostException(status, action);
        }
        return response ?? [];
    }

    /// <summary>
    /// Calls an operation and returns its payload (null when it has none). A domain error throws
    /// <see cref="RemoteException"/>; a host failure throws <see cref="HostException"/>.
    /// </summary>
    public JsonNode? Call(string contractName, string contractVersion, string operation, JsonNode? payload = null,
        TimeSpan? timeout = null, CancellationToken token = default)
    {
        var request = new JsonObject
        {
            ["contract"] = new JsonObject { ["name"] = contractName, ["version"] = contractVersion },
            ["operation"] = operation,
        };
        if (payload is not null)
        {
            request["payload"] = payload.DeepClone();
        }
        var response = JsonNode.Parse(Exchange(true, Encoding.UTF8.GetBytes(request.ToJsonString()), timeout, token, operation))!.AsObject();
        if (response["error"] is JsonObject error)
        {
            throw new RemoteException(
                error["code"]?.GetValue<string>() ?? "",
                error["message"]?.GetValue<string>() ?? "",
                error["retryAfterMilliseconds"]?.GetValue<long>());
        }
        return response["payload"]?.DeepClone();
    }

    /// <summary>Sends a complete request envelope (UTF-8 JSON) and returns the response envelope.</summary>
    public byte[] Invoke(ReadOnlySpan<byte> request, TimeSpan? timeout = null, CancellationToken token = default) =>
        Exchange(false, request, timeout, token, "invoke");

    /// <summary>Stops admission and waits for the calls in flight; a timeout reopens admission.</summary>
    public void Drain(TimeSpan? timeout = null, CancellationToken token = default)
    {
        var options = Options(timeout);
        var status = Run("drain", timeout, token, (h, cancel) =>
        {
            var o = options;
            o.Cancel = (void*)cancel;
            return api.Drain(h, &o);
        });
        if (status != 0)
        {
            throw new HostException(status, "drain");
        }
    }

    /// <summary>Aborts the session; calls in flight end with "closed". Thread-safe; harmless after Dispose.</summary>
    public void Close()
    {
        if (ReferenceEquals(inPolicy, this))
        {
            throw new HostException(1, "close");
        }
        lock (gate)
        {
            if (handle != 0)
            {
                api.Close(handle);
            }
        }
    }

    /// <summary>"created", "ready", "draining", "closed" or "failed".</summary>
    public string State
    {
        get
        {
            var h = Enter("state");
            try
            {
                var s = api.State(h);
                return s >= 0 && s < States.Length ? States[s] : "unknown";
            }
            finally
            {
                Leave();
            }
        }
    }

    /// <summary>What a process guest wrote to stderr, bounded and untrusted.</summary>
    public byte[] Stderr()
    {
        var h = Enter("stderr");
        try
        {
            var buffer = new byte[StderrCapacity];
            nuint length;
            int status;
            fixed (byte* data = buffer)
            {
                status = api.Stderr(h, data, StderrCapacity, &length);
            }
            if (status != 0)
            {
                throw new HostException(status, "stderr");
            }
            return buffer.AsSpan(0, (int)Math.Min(length, (nuint)StderrCapacity)).ToArray();
        }
        finally
        {
            Leave();
        }
    }

    /// <summary>True once the host has been freed.</summary>
    public bool Disposed
    {
        get
        {
            lock (gate)
            {
                return handle == 0;
            }
        }
    }

    /// <summary>Closes the session, waits for every call in flight to return, and frees the host. Once is enough.</summary>
    public void Dispose()
    {
        if (ReferenceEquals(inPolicy, this))
        {
            throw new HostException(1, "dispose");
        }
        nint h;
        lock (gate)
        {
            if (handle == 0 || disposing)
            {
                return;
            }
            disposing = true;
            api.Close(handle); // the calls in flight end
            while (active > 0)
            {
                Monitor.Wait(gate);
            }
            h = handle;
            handle = 0;
        }
        api.Destroy(h);
        if (self.IsAllocated)
        {
            self.Free();
        }
        GC.SuppressFinalize(this);
    }
}
