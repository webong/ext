using System.Security.Cryptography;
using System.Text.Json.Nodes;
using ExtIO.Plugin;
using Webong.Ext.System.Windows;

if (args.Length != 1 || args[0] != "run" || !OperatingSystem.IsWindows() ||
    Environment.ProcessPath is not { } binary)
    return 2;

string revision;
using (var executable = File.OpenRead(binary))
    revision = Convert.ToHexString(SHA256.HashData(executable)).ToLowerInvariant();

var descriptor = new JsonObject
{
    ["apiVersion"] = "ext.plugin/v1",
    ["identity"] = new JsonObject { ["id"] = "system.clipboard.windows", ["revision"] = revision, ["version"] = "1.0.0" },
    ["contracts"] = new JsonArray
    {
        new JsonObject
        {
            ["name"] = "ext.system.clipboard", ["version"] = "v1",
            ["operations"] = new JsonArray
            {
                new JsonObject { ["name"] = "status", ["surface"] = "observation" },
                new JsonObject { ["name"] = "generation", ["surface"] = "observation" },
                new JsonObject { ["name"] = "read", ["surface"] = "observation" },
                new JsonObject { ["name"] = "write", ["surface"] = "action" },
                new JsonObject { ["name"] = "clear", ["surface"] = "action" },
            },
        },
    },
};

new Guest(descriptor, Handle, maxCall: TimeSpan.FromSeconds(10), redactErrors: true).ServeStdio();
return 0;

static JsonNode? Handle(string contract, string operation, JsonNode? payload)
{
    if (contract != "ext.system.clipboard") throw new RemoteError("invalid", "unknown clipboard contract");
    var input = payload as JsonObject ?? new JsonObject();
    try
    {
        switch (operation)
        {
            case "status":
                var status = SystemClipboard.Status();
                return new JsonObject { ["state"] = status.State, ["reason"] = status.Reason };
            case "generation":
                return new JsonObject { ["generation"] = SystemClipboard.Generation() };
            case "read":
                var formats = Formats(input);
                var snapshot = SystemClipboard.Read(Policy(input, formats, read: true));
                var items = new JsonArray();
                foreach (var item in snapshot.Items)
                {
                    var representations = new JsonArray();
                    foreach (var value in item.Representations)
                        representations.Add(new JsonObject { ["format"] = value.Format,
                            ["data"] = Convert.ToBase64String(value.Data) });
                    items.Add(new JsonObject { ["representations"] = representations });
                }
                return new JsonObject { ["generation"] = snapshot.Generation, ["items"] = items };
            case "write":
                var requested = input["items"] as JsonArray ?? throw new RemoteError("invalid", "items are required");
                var values = new List<ClipboardItem>();
                var allowed = new HashSet<string>(StringComparer.Ordinal);
                foreach (var item in requested)
                {
                    var entries = item?["representations"] as JsonArray ?? throw new RemoteError("invalid", "representations are required");
                    var representations = new List<ClipboardRepresentation>();
                    foreach (var entry in entries)
                    {
                        var format = entry?["format"]?.GetValue<string>() ?? throw new RemoteError("invalid", "format is required");
                        var data = Convert.FromBase64String(entry?["data"]?.GetValue<string>() ?? "");
                        allowed.Add(format);
                        representations.Add(new ClipboardRepresentation(format, data));
                    }
                    values.Add(new ClipboardItem(representations));
                }
                var receipt = SystemClipboard.Write(Policy(input, allowed, write: true), Expected(input), values);
                return new JsonObject { ["generation"] = receipt.Generation, ["dispatch"] = receipt.Dispatch };
            case "clear":
                var cleared = SystemClipboard.Clear(Policy(input, new[] { "CF_UNICODETEXT" }, write: true), Expected(input));
                return new JsonObject { ["generation"] = cleared.Generation, ["dispatch"] = cleared.Dispatch };
            default:
                throw new RemoteError("unsupported", "unknown clipboard operation");
        }
    }
    catch (ClipboardException error) { throw new RemoteError(error.Code, error.Message); }
    catch (Exception error) when (error is FormatException or InvalidOperationException or ArgumentException)
    {
        throw new RemoteError("invalid", "invalid clipboard request");
    }
}

static string Expected(JsonObject input) => input["expected_generation"]?.GetValue<string>()
    ?? throw new RemoteError("invalid", "expected generation is required");

static string[] Formats(JsonObject input) => (input["formats"] as JsonArray
    ?? throw new RemoteError("invalid", "formats are required"))
    .Select(value => value?.GetValue<string>() ?? throw new RemoteError("invalid", "format is required"))
    .ToArray();

static ClipboardPolicy Policy(JsonObject input, IEnumerable<string> formats, bool read = false, bool write = false)
{
    var bytes = input["max_bytes"]?.GetValue<int>() ?? 16 << 20;
    return new ClipboardPolicy(formats.ToArray(), MaxBytes: bytes, AllowRead: read, AllowWrite: write).Validate();
}
