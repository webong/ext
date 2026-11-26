using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Nodes;
using Ext.Network.Windows;
using ExtIO.Plugin;

if (args.Length != 1 || args[0] != "run" || !OperatingSystem.IsWindows() ||
    Environment.ProcessPath is not { } binary)
    return 2;

string revision;
using (var executable = File.OpenRead(binary))
    revision = Convert.ToHexString(SHA256.HashData(executable)).ToLowerInvariant();

var descriptor = new JsonObject
{
    ["apiVersion"] = "ext.plugin/v1",
    ["identity"] = new JsonObject { ["id"] = "system.network.windows", ["revision"] = revision, ["version"] = "1.0.0" },
    ["contracts"] = new JsonArray
    {
        new JsonObject
        {
            ["name"] = "ext.system.network", ["version"] = "v1",
            ["operations"] = new JsonArray
            {
                new JsonObject { ["name"] = "observe", ["surface"] = "observation" },
            },
        },
    },
};

new Guest(descriptor, Handle, maxCall: TimeSpan.FromSeconds(10), redactErrors: true).ServeStdio();
return 0;

static JsonNode? Handle(string contract, string operation, JsonNode? payload)
{
    if (contract != "ext.system.network" || operation != "observe")
        throw new RemoteError("unsupported", "unknown network operation");
    try
    {
        var input = payload as JsonObject ?? new JsonObject();
        var limits = new NetworkOptions(
            MaxInterfaces: input["max_interfaces"]?.GetValue<int>() ?? 256,
            MaxAddresses: input["max_addresses"]?.GetValue<int>() ?? 256,
            MaxRoutes: input["max_routes"]?.GetValue<int>() ?? 4096,
            Timeout: TimeSpan.FromMilliseconds(input["timeout_ms"]?.GetValue<int>() ?? 5000));
        var snapshot = LocalNetwork.Discover(limits);
        return JsonSerializer.SerializeToNode(snapshot, new JsonSerializerOptions
        {
            PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        });
    }
    catch (ArgumentException)
    {
        throw new RemoteError("invalid", "invalid network options");
    }
    catch (OperationCanceledException)
    {
        throw new RemoteError("timeout", "network observation timed out");
    }
}
