// The guest: an immutable descriptor plus a domain handler, served over JSON lines.
using System.Numerics;
using System.Text;
using System.Text.Json.Nodes;

namespace ExtIO.Plugin;

/// <summary>
/// A domain handler: given the contract name, the operation and the request payload, it returns a
/// JSON result (or null), or throws <see cref="RemoteError"/> for a deliberate public failure. The
/// call's details are in <see cref="Guest.CurrentCall"/>.
/// </summary>
public delegate JsonNode? Handler(string contract, string operation, JsonNode? payload);

/// <summary>
/// Details of the call being handled, for handlers that need them. On a transport that carries
/// frames (a guest served by <see cref="Guest.Serve"/>) the call can also report progress and call
/// its host: see <see cref="Progress"/>, <see cref="ProgressLimits"/> and <see cref="Reverse"/>.
/// This guest reads inline, so a handler makes one reverse call at a time, from its own thread.
/// </summary>
public sealed class CallInfo
{
    readonly long deadline;
    internal Frames.Reporter? Reporter { get; set; }

    public string RequestId { get; }
    public string ContractName { get; }
    public string ContractVersion { get; }
    public string Operation { get; }
    public string Surface { get; }

    /// <summary>The rate the host set for this call's events, or null when the operation reports none.</summary>
    public ProgressLimits? ProgressLimits { get; internal set; }

    /// <summary>A caller for host operations, bound by the call's deadline, or null on a transport with no frames.</summary>
    public Frames.ReverseCaller? Reverse { get; internal set; }

    internal CallInfo(string requestId, string contractName, string contractVersion, string operation, string surface, long deadline)
    {
        RequestId = requestId;
        ContractName = contractName;
        ContractVersion = contractVersion;
        Operation = operation;
        Surface = surface;
        this.deadline = deadline;
    }

    /// <summary>When the call must be answered.</summary>
    public DateTimeOffset Deadline => Wire.ToTime(deadline);

    /// <summary>Time left before the deadline (negative once it has passed).</summary>
    public TimeSpan Remaining => TimeSpan.FromTicks((deadline - Wire.NowMicros()) * 10);

    /// <summary>
    /// Reports a progress event to the host while the call runs. Events reach the host in order,
    /// before the result. Returns <see cref="ProgressResult.Dropped"/> when the rate limit dropped it.
    /// Throws <see cref="FrameError"/>: <c>unsupported</c> when the operation did not declare
    /// progress, the host asked for no events or there is no frame channel (stop reporting),
    /// <c>invalid</c> for an event that breaks the rules (nothing is sent), <c>closed</c> once the
    /// call is over.
    /// </summary>
    public ProgressResult Progress(ExtIO.Plugin.Progress e) =>
        Reporter is null ? throw new FrameError("unsupported", "no progress channel on this call") : Reporter.Report(e);
}

/// <summary>
/// An ext.io.pc/v1 guest. It answers <c>session.hello</c> with the descriptor, validates every
/// request the way the Go <c>plugin.ValidateRequest</c> does, and calls the handler sequentially.
/// Any failure but a <see cref="RemoteError"/> becomes <c>provider_error</c> with the message cut to
/// 4096 bytes; with <c>redactErrors</c> it becomes a generic <c>operation_failed</c> instead, so
/// internal text never reaches the host.
/// </summary>
public sealed class Guest
{
    public static readonly TimeSpan DefaultMaxCall = TimeSpan.FromSeconds(30);

    static readonly string[] DescriptorFields = { "apiVersion", "identity", "contracts", "reverse" };
    static readonly string[] HelloFields = { "apiVersion", "id", "plugin", "contract", "operation", "surface", "deadline", "payload" };
    static readonly string[] RequestFields = { "apiVersion", "id", "plugin", "contract", "operation", "surface", "deadline", "payload", "progress", "parent" };
    static readonly string[] PluginFields = { "id", "revision", "version" };
    static readonly string[] ContractFields = { "name", "version" };
    static readonly BigInteger Int64Min = long.MinValue;
    static readonly BigInteger Int64Max = long.MaxValue;

    [ThreadStatic] static CallInfo? current;

    readonly JsonObject descriptor;
    readonly Handler handler;
    readonly long maxCallMicros;
    readonly bool redact;
    readonly Dictionary<(string, string, string), (string Surface, bool Progress)> ops = new();
    readonly HashSet<(string, string, string)> reverse = new();
    readonly (string Id, string Revision, string Version) identity;

    /// <summary>The <see cref="CallInfo"/> of the in-flight call, or null outside a handler.</summary>
    public static CallInfo? CurrentCall => current;

    public Guest(JsonObject descriptor, Handler handler, TimeSpan? maxCall = null, bool redactErrors = false)
        : this(CopyDescriptor(descriptor), handler, maxCall, redactErrors, true) { }

    /// <summary>A guest whose descriptor is in a plugin.json (ext.io/v1) or a bare descriptor file.</summary>
    public Guest(string descriptorPath, Handler handler, TimeSpan? maxCall = null, bool redactErrors = false)
        : this(LoadDescriptor(descriptorPath), handler, maxCall, redactErrors, true) { }

    Guest(JsonObject descriptor, Handler handler, TimeSpan? maxCall, bool redactErrors, bool _)
    {
        this.handler = handler ?? throw new ArgumentNullException(nameof(handler));
        var max = maxCall ?? DefaultMaxCall;
        if (max <= TimeSpan.Zero) throw new ArgumentException("maxCall must be positive");
        this.descriptor = descriptor;
        maxCallMicros = max.Ticks / 10;
        redact = redactErrors;
        foreach (var c in descriptor["contracts"]!.AsArray())
        {
            foreach (var op in c!["operations"]!.AsArray())
            {
                ops[(Wire.AsString(c["name"])!, Wire.AsString(c["version"])!, Wire.AsString(op!["name"])!)] =
                    (Wire.AsString(op["surface"]) ?? "", Wire.AsBool(op["progress"]) == true);
            }
        }
        if (descriptor["reverse"] is JsonArray rs)
        {
            foreach (var c in rs)
            {
                foreach (var op in c!["operations"]!.AsArray())
                {
                    reverse.Add((Wire.AsString(c["name"])!, Wire.AsString(c["version"])!, Wire.AsString(op!["name"])!));
                }
            }
        }
        var ident = descriptor["identity"]!;
        identity = (Wire.AsString(ident["id"])!, Wire.AsString(ident["revision"])!, Wire.AsString(ident["version"]) ?? "");
    }

    static JsonObject CopyDescriptor(JsonObject descriptor)
    {
        JsonNode? copy;
        try
        {
            copy = Wire.Copy(descriptor);
        }
        catch (EncodeError e)
        {
            throw new ArgumentException("descriptor is not JSON: " + e.Message);
        }
        ValidateDescriptor(copy);
        return (JsonObject)copy!;
    }

    /// <summary>The descriptor the guest answers <c>session.hello</c> with.</summary>
    public JsonObject Handshake() => (JsonObject)descriptor.DeepClone();

    // --- descriptor --------------------------------------------------------------------------

    /// <summary>Throws <see cref="ArgumentException"/> unless the value is a valid ext.io.pc/v1 descriptor.</summary>
    public static void ValidateDescriptor(JsonNode? d)
    {
        if (!Wire.OnlyKeys(d, DescriptorFields))
        {
            throw new ArgumentException("descriptor must be an object with apiVersion, identity, contracts");
        }
        if (Wire.AsString(d!["apiVersion"]) != Wire.ApiVersion) throw new ArgumentException("unsupported apiVersion");
        var ident = d["identity"];
        if (!Wire.OnlyKeys(ident, PluginFields)) throw new ArgumentException("invalid identity");
        if (!Wire.Matches(Wire.Identifier, Wire.AsString(ident!["id"])) || !Wire.Matches(Wire.Revision, Wire.AsString(ident["revision"])))
        {
            throw new ArgumentException("identity requires a bounded id and immutable revision");
        }
        var obj = (JsonObject)ident;
        if (obj.ContainsKey("version"))
        {
            var version = Wire.AsString(ident["version"]);
            if (version != "" && !Wire.Matches(Wire.Revision, version)) throw new ArgumentException("invalid identity version");
        }
        if (d["contracts"] is not JsonArray contracts || contracts.Count < 1 || contracts.Count > 64)
        {
            throw new ArgumentException("declare 1..64 contracts");
        }
        ValidateContracts(contracts, false);
        // The host operations the plugin may call; none is a null or empty list.
        var rev = d["reverse"];
        if (rev is not null)
        {
            if (rev is not JsonArray list || list.Count > 64) throw new ArgumentException("declare at most 64 reverse contracts");
            ValidateContracts(list, true);
        }
    }

    static void ValidateContracts(JsonArray contracts, bool isReverse)
    {
        var seen = new HashSet<(string, string)>();
        foreach (var c in contracts)
        {
            if (!Wire.OnlyKeys(c, "name", "version", "operations")) throw new ArgumentException("invalid contract");
            var name = Wire.AsString(c!["name"]);
            var version = Wire.AsString(c["version"]);
            if (!Wire.Matches(Wire.Identifier, name) || !Wire.Matches(Wire.Revision, version))
            {
                throw new ArgumentException("contract requires a name and exact version");
            }
            if (!seen.Add((name!, version!)) || c["operations"] is not JsonArray ops || ops.Count < 1 || ops.Count > 256)
            {
                throw new ArgumentException("duplicate contract or invalid operation count");
            }
            var names = new HashSet<string>();
            foreach (var op in ops)
            {
                if (!Wire.OnlyKeys(op, "name", "surface", "progress")) throw new ArgumentException("invalid operation");
                var opName = Wire.AsString(op!["name"]);
                var surfaceNode = op["surface"];
                var progressNode = op["progress"];
                // Go leaves a member that is null alone, as if absent.
                var surface = surfaceNode is null ? "" : Wire.AsString(surfaceNode);
                var progress = progressNode is null ? (bool?)null : Wire.AsBool(progressNode);
                if (!Wire.Matches(Wire.Identifier, opName) ||
                    opName == "session.hello" ||
                    !names.Add(opName!) ||
                    surface is null ||
                    (surface != "" && !Wire.Matches(Wire.Identifier, surface)) ||
                    (progressNode is not null && progress is null) ||
                    (progress == true && isReverse)) // a host operation does not report progress
                {
                    throw new ArgumentException("invalid, reserved, or duplicate operation");
                }
            }
        }
    }

    /// <summary>Reads a descriptor from a plugin.json (ext.io/v1) or a bare descriptor file.</summary>
    public static JsonObject LoadDescriptor(string path)
    {
        byte[] raw;
        using (var f = File.OpenRead(path))
        {
            if (f.Length > 16 << 20) throw new ArgumentException("descriptor file exceeds 16 MiB");
            raw = new byte[f.Length];
            f.ReadExactly(raw);
        }
        JsonNode? data;
        try
        {
            data = Wire.Decode(raw);
        }
        catch (ProtocolError e)
        {
            throw new ArgumentException(e.Message);
        }
        if (data is JsonObject o && Wire.AsString(o["apiVersion"]) == "ext.io/v1")
        {
            if (Wire.AsString(o["kind"]) != "package" || o.ContainsKey("descriptor")) throw new ArgumentException("invalid ext.io/v1 package");
            var descriptor = new JsonObject { ["apiVersion"] = "ext.io.pc/v1" };
            foreach (var key in new[] { "identity", "contracts", "reverse" })
                if (o.ContainsKey(key)) descriptor[key] = o[key]?.DeepClone();
            data = descriptor;
        }
        ValidateDescriptor(data);
        return (JsonObject)data!;
    }

    // --- requests ----------------------------------------------------------------------------

    // The rate a request sets for a call's progress events: null when absent, else the members
    // with 0 for one left out. Go decodes them into int64s, so a number that is not a whole one
    // that fits is not a request at all.
    static (long, long)? RateOf(JsonNode? value)
    {
        if (value is null) return null;
        if (!Wire.OnlyKeys(value, "eventsPerSecond", "bytesPerSecond")) throw new ProtocolError("invalid progress rate");
        long Member(string key)
        {
            var n = value[key];
            if (n is null) return 0;
            if (Wire.AsInteger(n) is { } i && i >= Int64Min && i <= Int64Max) return (long)i;
            throw new ProtocolError("invalid progress rate");
        }
        return (Member("eventsPerSecond"), Member("bytesPerSecond"));
    }

    static string[] IdentityOf(JsonNode? obj, string[] fields)
    {
        var values = new string[fields.Length];
        if (obj is null)
        {
            Array.Fill(values, "");
            return values;
        }
        if (!Wire.OnlyKeys(obj, fields)) throw new ProtocolError("invalid envelope object");
        var o = (JsonObject)obj;
        for (var k = 0; k < fields.Length; k++)
        {
            if (!o.TryGetPropertyValue(fields[k], out var v))
            {
                values[k] = "";
                continue;
            }
            values[k] = Wire.AsString(v) ?? throw new ProtocolError("invalid envelope field");
        }
        return values;
    }

    /// <summary>
    /// Validates one decoded request and returns the response object. Throws
    /// <see cref="ProtocolError"/> for envelopes the Go host would not even decode (unknown fields,
    /// wrong types); those end the stream. <paramref name="link"/> is the connection the request
    /// came in on: without one the call can neither report progress nor call its host.
    /// </summary>
    public JsonObject Invoke(JsonNode? request, Frames.Link? link = null)
    {
        if (!Wire.OnlyKeys(request, RequestFields)) throw new ProtocolError("invalid request envelope");
        var r = (JsonObject)request!;
        var requestId = Wire.AsString(r["id"]) ?? throw new ProtocolError("invalid request id");
        var plugin = IdentityOf(r["plugin"], PluginFields);
        var contract = IdentityOf(r["contract"], ContractFields);
        foreach (var k in new[] { "apiVersion", "operation", "surface", "deadline" })
        {
            if (r.TryGetPropertyValue(k, out var v) && Wire.AsString(v) is null) throw new ProtocolError("invalid request field");
        }
        if (r["parent"] is { } parent && Wire.AsString(parent) is null) throw new ProtocolError("invalid request parent");
        var rate = RateOf(r["progress"]);

        var response = new JsonObject { ["apiVersion"] = Wire.ApiVersion, ["id"] = requestId };
        var deadline = Wire.ParseDeadline(r["deadline"]);
        var operation = Wire.AsString(r["operation"]) ?? "";
        var surface = Wire.AsString(r["surface"]) ?? "";
        var key = (contract[0], contract[1], operation);
        if (Wire.AsString(r["apiVersion"]) != Wire.ApiVersion ||
            !Wire.Matches(Wire.Revision, requestId) ||
            deadline is null ||
            (plugin[0], plugin[1], plugin[2]) != identity ||
            !ops.TryGetValue(key, out var op) ||
            op.Surface != surface)
        {
            response["error"] = new JsonObject { ["code"] = "invalid_request", ["message"] = "request does not match selected contract" };
            return response;
        }

        var limit = Math.Min(deadline.Value, Wire.NowMicros() + maxCallMicros);
        JsonNode? error = null;
        JsonNode? result = null;
        if (Wire.NowMicros() >= limit)
        {
            error = Failure("deadline exceeded");
        }
        else
        {
            var info = new CallInfo(requestId, contract[0], contract[1], operation, surface, limit);
            current = info;
            Frames.Reporter? reporter = null;
            Frames.ReverseCaller? caller = null;
            if (link is not null)
            {
                // A call reports progress when its operation declared it and the host asked for
                // events; it can call the host on the connection it came in on.
                if (rate is { } rt && op.Progress)
                {
                    info.ProgressLimits = new ProgressLimits(
                        rt.Item1 > 0 ? rt.Item1 : ExtIO.Plugin.Progress.DefaultEvents,
                        rt.Item2 > 0 ? rt.Item2 : ExtIO.Plugin.Progress.DefaultBytes);
                    reporter = new Frames.Reporter(link, requestId, info.ProgressLimits);
                    info.Reporter = reporter;
                }
                caller = info.Reverse = new Frames.ReverseCaller(link, reverse, requestId, limit);
            }
            try
            {
                // The handler gets the payload detached from the request, free to keep or return.
                var payload = r["payload"];
                r.Remove("payload");
                result = handler(contract[0], operation, payload);
                if (result?.Parent is not null) result = result.DeepClone();
                Wire.Encode(result); // surface unserializable results as provider errors
                if (Wire.NowMicros() >= limit) error = Failure("deadline exceeded");
            }
            catch (RemoteError e)
            {
                error = e.ToWire();
            }
            catch (Exception e)
            {
                error = Failure(e.Message);
            }
            finally
            {
                current = null;
                if (caller is not null) caller.Over = true; // a request names its call, so a caller cannot outlive it
                reporter?.Finish(); // a marker for events never reported, before the response
            }
        }
        if (error is null)
        {
            response["payload"] = result;
        }
        else
        {
            response["error"] = error;
        }
        return response;
    }

    JsonObject Failure(string message)
    {
        if (redact)
        {
            return new JsonObject { ["code"] = "operation_failed", ["message"] = "plugin operation failed" };
        }
        // Lone surrogates become '?', and the text is cut to 4096 bytes on a character boundary.
        var sb = new StringBuilder();
        var size = 0;
        for (var i = 0; i < message.Length;)
        {
            string piece;
            if (Rune.TryGetRuneAt(message, i, out var rune))
            {
                piece = rune.ToString();
                i += rune.Utf16SequenceLength;
            }
            else
            {
                piece = "?";
                i++;
            }
            var n = Encoding.UTF8.GetByteCount(piece);
            if (size + n > Wire.MaxMessageBytes) break;
            sb.Append(piece);
            size += n;
        }
        return new JsonObject { ["code"] = "provider_error", ["message"] = sb.ToString() };
    }

    // --- serving -----------------------------------------------------------------------------

    /// <summary>
    /// Serves the guest over binary streams until the input ends. Throws <see cref="ProtocolError"/>
    /// on violations. A handler can report progress and call its host (see <see cref="CallInfo"/>).
    /// The host sends nothing during a call but the answer to a reverse call: anything else ends the
    /// session with <see cref="ProtocolError"/>, and the call in flight is not answered.
    /// </summary>
    public void Serve(Stream input, Stream output)
    {
        var reader = new Wire.FrameReader(input);
        var link = new Frames.Link(reader, output);
        var hello = false;
        while (true)
        {
            var raw = reader.Read();
            if (raw is null) return;
            var request = Wire.Decode(raw);
            JsonObject response;
            if (hello)
            {
                response = Invoke(request, link);
                if (link.Failure is not null) throw link.Failure; // the host broke the rules while the handler waited
            }
            else
            {
                response = AnswerHello(request);
                hello = true;
            }
            var frame = Wire.Encode(response);
            if (frame.Length > Wire.MaxFrameBytes)
            {
                frame = Wire.Encode(new JsonObject
                {
                    ["apiVersion"] = Wire.ApiVersion,
                    ["id"] = Wire.AsString(response["id"]),
                    ["error"] = new JsonObject { ["code"] = "response_too_large", ["message"] = "response exceeds the frame limit" },
                });
            }
            link.Write(frame);
        }
    }

    JsonObject AnswerHello(JsonNode? request)
    {
        if (!Wire.OnlyKeys(request, HelloFields)) throw new ProtocolError("invalid hello");
        var r = (JsonObject)request!;
        var plugin = IdentityOf(r["plugin"], PluginFields);
        var contract = IdentityOf(r["contract"], ContractFields);
        var deadline = Wire.ParseDeadline(r["deadline"]);
        var hasSurface = r.TryGetPropertyValue("surface", out var surface);
        if (Wire.AsString(r["apiVersion"]) != Wire.ApiVersion ||
            Wire.AsString(r["id"]) != "hello" ||
            Wire.AsString(r["operation"]) != "session.hello" ||
            plugin.Any(v => v != "") ||
            contract.Any(v => v != "") ||
            (hasSurface && Wire.AsString(surface) != "") ||
            r.ContainsKey("payload") ||
            deadline is null)
        {
            throw new ProtocolError("invalid hello");
        }
        if (Wire.NowMicros() >= deadline) throw new ProtocolError("hello deadline exceeded");
        return new JsonObject { ["apiVersion"] = Wire.ApiVersion, ["id"] = "hello", ["payload"] = Handshake() };
    }

    /// <summary>
    /// Serves on this process's standard input and output and returns at the end of input. Protocol
    /// frames use the original standard output; <see cref="Console.Out"/> is redirected to standard
    /// error, so stray output from <c>Console.Write</c> cannot corrupt the stream. Call once, from
    /// the guest's main.
    /// </summary>
    public void ServeStdio()
    {
        Console.Out.Flush();
        using var output = Console.OpenStandardOutput();
        Console.SetOut(Console.Error);
        using var input = Console.OpenStandardInput();
        Serve(input, output);
    }

    /// <summary>Builds a guest from a descriptor file and serves it on standard input and output.</summary>
    public static void ServeStdio(string descriptorPath, Handler handler, TimeSpan? maxCall = null, bool redactErrors = false) =>
        new Guest(descriptorPath, handler, maxCall, redactErrors).ServeStdio();
}
