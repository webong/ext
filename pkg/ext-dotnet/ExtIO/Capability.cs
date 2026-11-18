// Typed definitions of the SDK's optional capability contracts, with the same wire shapes and
// limits as the Go library: ext.health, ext.configuration and ext.stream. Router is a handler
// that checks each payload and result against them before and after your code runs.
//
//     var router = new Capability.Router()
//         .Health(() => new JsonObject { ["status"] = "healthy", ["ready"] = true });
//     Guest.ServeStdio("plugin.json", router.Handle);
using System.Numerics;
using System.Text.Json.Nodes;

namespace ExtIO.Plugin;

public static class Capability
{
    public const int MaxStreamItems = 256;
    public const int MaxStreamBatchBytes = 1 << 20;
    public const int MaxId = 64;
    public const int MaxName = 256;
    static readonly string[] Statuses = { "healthy", "degraded", "unhealthy" };

    /// <summary>The value must be an object with exactly these keys.</summary>
    static JsonObject Exactly(JsonNode? value, params string[] fields)
    {
        if (value is not JsonObject o || o.Count != fields.Length || !fields.All(o.ContainsKey))
        {
            throw new ArgumentException($"object does not have exactly the fields [{string.Join(", ", fields)}]");
        }
        return o;
    }

    static string Text(JsonNode? value, int limit)
    {
        var s = Wire.AsString(value);
        if (s is not null && s.Length > 0 && s.EnumerateRunes().Count() <= limit) return s;
        throw new ArgumentException($"expected a non-empty string of at most {limit} characters");
    }

    static BigInteger Integer(JsonNode? value) => Wire.AsInteger(value) ?? throw new ArgumentException("expected an integer");

    public static void HealthRequest(JsonNode? value) => Exactly(value);

    public static void Health(JsonNode? value)
    {
        var o = Exactly(value, "status", "ready");
        if (Array.IndexOf(Statuses, Wire.AsString(o["status"])) < 0 || Wire.AsBool(o["ready"]) is null)
        {
            throw new ArgumentException("invalid health");
        }
    }

    public static void Configuration(JsonNode? value)
    {
        var o = Exactly(value, "instance", "revision", "values");
        Text(o["instance"], MaxName);
        Text(o["revision"], MaxName);
    }

    public static void Configured(JsonNode? value)
    {
        var o = Exactly(value, "instance", "revision");
        if (Wire.AsString(o["instance"]) is null || Wire.AsString(o["revision"]) is null) throw new ArgumentException("invalid configured");
    }

    public static void StreamOpen(JsonNode? value) => Exactly(value, "parameters");

    public static void StreamOpened(JsonNode? value) => Text(Exactly(value, "id")["id"], MaxId);

    public static void StreamRead(JsonNode? value)
    {
        var o = Exactly(value, "id", "sequence", "limit");
        Text(o["id"], MaxId);
        var sequence = Integer(o["sequence"]);
        var limit = Integer(o["limit"]);
        if (sequence < 1 || limit < 1 || limit > MaxStreamItems) throw new ArgumentException("invalid read");
    }

    public static void StreamClose(JsonNode? value) => Text(Exactly(value, "id")["id"], MaxId);

    public static void StreamClosed(JsonNode? value) => Exactly(value);

    /// <summary>At most <see cref="MaxStreamItems"/> items and <see cref="MaxStreamBatchBytes"/> of JSON.</summary>
    public static void StreamBatch(JsonNode? value)
    {
        var o = Exactly(value, "items", "done");
        if (o["items"] is not JsonArray items || Wire.AsBool(o["done"]) is null) throw new ArgumentException("invalid batch");
        if (items.Count > MaxStreamItems) throw new ArgumentException("too many items");
        var size = 0;
        foreach (var item in items)
        {
            size += Wire.Utf8Size(item);
            if (size > MaxStreamBatchBytes) throw new ArgumentException("batch too large");
        }
    }

    /// <summary>
    /// Dispatches (contract, operation) to registered functions, validating the payload before the
    /// call and the result after it. A payload that fails validation is an <c>invalid_input</c>
    /// remote error; a result that fails is an internal error, because it is the plugin's own bug.
    /// </summary>
    public sealed class Router
    {
        readonly Dictionary<(string, string, string), (Func<JsonNode?, JsonNode?> Run, Action<JsonNode?>? In, Action<JsonNode?>? Out)> routes = new();

        /// <summary>
        /// Registers <paramref name="function"/>, called with the payload, for an operation of a
        /// contract at an exact version. The checks throw <see cref="ArgumentException"/> when a value
        /// does not fit.
        /// </summary>
        public Router On(string contract, string version, string operation, Func<JsonNode?, JsonNode?> function,
            Action<JsonNode?>? checkInput = null, Action<JsonNode?>? checkOutput = null)
        {
            ArgumentNullException.ThrowIfNull(function);
            if (!routes.TryAdd((contract, version, operation), (function, checkInput, checkOutput)))
            {
                throw new ArgumentException("operation already registered");
            }
            return this;
        }

        /// <summary><paramref name="check"/> returns {"status": ..., "ready": ...}.</summary>
        public Router Health(Func<JsonNode?> check) =>
            On("ext.health", "v1", "check", _ => check(), HealthRequest, Capability.Health);

        /// <summary><paramref name="update"/> takes (instance, revision, values); the router answers.</summary>
        public Router Configuration(Action<string, string, JsonNode?> update) =>
            On("ext.configuration", "v1", "update", p =>
            {
                var instance = Wire.AsString(p!["instance"])!;
                var revision = Wire.AsString(p["revision"])!;
                update(instance, revision, p["values"]);
                return new JsonObject { ["instance"] = instance, ["revision"] = revision };
            }, Capability.Configuration, Configured);

        /// <summary>
        /// <paramref name="open"/> takes the parameters and returns an id; <paramref name="read"/>
        /// takes (id, sequence, limit) and returns {"items": [...], "done": ...};
        /// <paramref name="close"/> takes the id.
        /// </summary>
        public Router Stream(Func<JsonNode?, string> open, Func<string, long, int, JsonNode?> read, Action<string> close)
        {
            On("ext.stream", "v1", "open", p => new JsonObject { ["id"] = open(p!["parameters"]) }, StreamOpen, StreamOpened);
            On("ext.stream", "v1", "read", p =>
                read(Wire.AsString(p!["id"])!, (long)Wire.AsInteger(p["sequence"])!.Value, (int)Wire.AsInteger(p["limit"])!.Value),
                StreamRead, StreamBatch);
            On("ext.stream", "v1", "close", p =>
            {
                close(Wire.AsString(p!["id"])!);
                return new JsonObject();
            }, StreamClose, StreamClosed);
            return this;
        }

        /// <summary>The handler: the contract's version is the current call's, or v1 outside one.</summary>
        public JsonNode? Handle(string contract, string operation, JsonNode? payload)
        {
            var version = Guest.CurrentCall?.ContractVersion ?? "v1";
            if (!routes.TryGetValue((contract, version, operation), out var route))
            {
                throw new RemoteError("unsupported", "operation is not implemented");
            }
            try
            {
                route.In?.Invoke(payload);
            }
            catch (ArgumentException)
            {
                throw new RemoteError("invalid_input", "request payload does not satisfy operation contract");
            }
            var result = route.Run(payload);
            if (route.Out is null) return result;
            // A failure here is the plugin's bug and surfaces as an internal error.
            result = Wire.Copy(result);
            route.Out(result);
            return result;
        }
    }
}
