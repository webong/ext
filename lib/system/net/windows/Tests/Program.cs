using System.Net;
using System.Text.Json;
using Ext.Network.Windows;

static void Check(bool condition, string message)
{
    if (!condition) throw new Exception(message);
}

foreach (var value in new[] { 0, -1, 65537 })
{
    try
    {
        new NetworkOptions(MaxInterfaces: value).Validate();
        throw new Exception($"accepted interface bound {value}");
    }
    catch (ArgumentOutOfRangeException) { }
}
new NetworkOptions().Validate();
Check(LocalNetwork.Scope(IPAddress.Parse("127.1.2.3")) == "loopback", "loopback scope");
Check(LocalNetwork.Scope(IPAddress.Parse("169.254.1.1")) == "link", "link scope");
Check(LocalNetwork.Scope(IPAddress.Parse("192.168.1.1")) == "private", "private scope");
Check(LocalNetwork.Scope(IPAddress.Parse("100.64.1.1")) == "global", "shared address scope");
Check(LocalNetwork.Scope(IPAddress.Parse("ff02::1")) == "link", "IPv6 link multicast scope");
Check(LocalNetwork.Scope(IPAddress.Parse("fc00::1")) == "private", "IPv6 private scope");
Check(LocalNetwork.Distinct(new[] { " a ", "a", " b ", "" }).SequenceEqual(new[] { "a", "b" }), "resolver order and deduplication");

static string FixturePath()
{
    for (var directory = new DirectoryInfo(Directory.GetCurrentDirectory()); directory is not null; directory = directory.Parent)
    {
        var path = Path.Combine(directory.FullName, "res", "system", "testdata", "network", "v1", "cases.json");
        if (File.Exists(path)) return path;
    }
    throw new FileNotFoundException("network fixtures are unavailable");
}

static string[] Strings(JsonElement parent, string name) => parent.GetProperty(name).EnumerateArray()
    .Select(value => value.GetString() ?? "").ToArray();

using (var fixture = JsonDocument.Parse(File.ReadAllText(FixturePath())))
{
    var root = fixture.RootElement;
    foreach (var item in root.GetProperty("options").EnumerateArray())
    {
        var invalid = item.GetProperty("code").ValueKind != JsonValueKind.Null;
        try
        {
            new NetworkOptions(item.GetProperty("maxInterfaces").GetInt32(),
                item.GetProperty("maxAddresses").GetInt32(), item.GetProperty("maxRoutes").GetInt32()).Validate();
            Check(!invalid, $"accepted invalid options {item.GetProperty("name")}");
        }
        catch (ArgumentOutOfRangeException) { Check(invalid, $"rejected valid options {item.GetProperty("name")}"); }
    }
    foreach (var item in root.GetProperty("addresses").EnumerateArray())
    {
        var invalid = item.GetProperty("code").ValueKind != JsonValueKind.Null;
        try
        {
            var actual = LocalNetwork.ParseAddress(item.GetProperty("ip").GetString() ?? "", item.GetProperty("prefix").GetInt32());
            Check(!invalid, $"accepted invalid address {item.GetProperty("name")}");
            var want = item.GetProperty("want");
            Check(actual.IP == want.GetProperty("ip").GetString() &&
                actual.PrefixLength == want.GetProperty("prefixLength").GetInt32() &&
                actual.Family == want.GetProperty("family").GetString() &&
                actual.Scope == want.GetProperty("scope").GetString(), $"address {item.GetProperty("name")}: {actual}");
        }
        catch (ArgumentException) { Check(invalid, $"rejected valid address {item.GetProperty("name")}"); }
    }
    foreach (var item in root.GetProperty("resolver").EnumerateArray())
    {
        var want = item.GetProperty("want");
        foreach (var name in new[] { "servers", "search", "options" })
            Check(LocalNetwork.Distinct(Strings(item, name)).SequenceEqual(Strings(want, name)),
                $"resolver {item.GetProperty("name")}/{name}");
    }
}
if (OperatingSystem.IsWindows())
{
    var snapshot = LocalNetwork.Discover(new NetworkOptions());
    Check(snapshot.Platform == "windows", "platform");
    Check(snapshot.InterfaceStatus.State is "complete" or "partial" or "unavailable", "interface status");
    Check(snapshot.RouteStatus.State == "unsupported", "route status");
}
Console.WriteLine("ok   Windows network contract");
