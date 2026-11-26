using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;

namespace Ext.Network.Windows;

public sealed record NetworkOptions(int MaxInterfaces = 256, int MaxAddresses = 256,
    int MaxRoutes = 4096, TimeSpan? Timeout = null)
{
    public TimeSpan EffectiveTimeout => Timeout ?? TimeSpan.FromSeconds(5);

    public void Validate()
    {
        if (MaxInterfaces is < 1 or > 65536 || MaxAddresses is < 1 or > 65536 ||
            MaxRoutes is < 1 or > 65536 || EffectiveTimeout <= TimeSpan.Zero)
            throw new ArgumentOutOfRangeException(nameof(NetworkOptions), "network bounds must be positive and at most 65536");
    }
}

public sealed record CollectionStatus(string State, string? Reason = null, string? Detail = null,
    DateTimeOffset? ObservedAt = null);

public sealed record NetworkAddress(string IP, int PrefixLength, string Family, string Scope);

public sealed record NetworkInterfaceInfo(int Index, string Name, int MTU, string HardwareAddress,
    IReadOnlyList<string> Flags, IReadOnlyList<NetworkAddress> Addresses);

public sealed record NetworkRoute(string Destination, string Gateway, string Interface, int Metric, bool Default);

public sealed record NetworkResolver(IReadOnlyList<string> Servers, IReadOnlyList<string> Search,
    IReadOnlyList<string> Options);

public sealed record NetworkSnapshot(string Platform, DateTimeOffset ObservedAt,
    IReadOnlyList<NetworkInterfaceInfo> Interfaces, IReadOnlyList<NetworkRoute> Routes,
    NetworkResolver Resolver, CollectionStatus InterfaceStatus, CollectionStatus RouteStatus,
    CollectionStatus ResolverStatus);

/// <summary>Reads local Windows network state without resolving names or opening a connection.</summary>
public static class LocalNetwork
{
    public static NetworkSnapshot Discover(NetworkOptions options, CancellationToken cancellation = default)
    {
        if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException("Windows network observation requires Windows");
        options.Validate();
        cancellation.ThrowIfCancellationRequested();
        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(cancellation);
        deadline.CancelAfter(options.EffectiveTimeout);
        var token = deadline.Token;
        var now = DateTimeOffset.UtcNow;
        var interfaces = new List<NetworkInterfaceInfo>();
        var servers = new List<string>();
        var search = new List<string>();
        bool partial = false;
        CollectionStatus interfaceStatus;
        CollectionStatus resolverStatus;
        try
        {
            var native = NetworkInterface.GetAllNetworkInterfaces();
            foreach (var device in native.OrderBy(x => x.GetIPProperties().GetIPv4Properties()?.Index ?? int.MaxValue))
            {
                token.ThrowIfCancellationRequested();
                if (interfaces.Count >= options.MaxInterfaces) { partial = true; break; }
                var properties = device.GetIPProperties();
                var addresses = new List<NetworkAddress>();
                foreach (var assigned in properties.UnicastAddresses)
                {
                    token.ThrowIfCancellationRequested();
                    if (addresses.Count >= options.MaxAddresses) { partial = true; break; }
                    if (assigned.Address.AddressFamily is not (AddressFamily.InterNetwork or AddressFamily.InterNetworkV6)) continue;
                    var literal = assigned.Address.AddressFamily == AddressFamily.InterNetworkV6
                        ? new IPAddress(assigned.Address.GetAddressBytes()).ToString()
                        : assigned.Address.ToString();
                    addresses.Add(ParseAddress(literal, assigned.PrefixLength));
                }
                foreach (var server in properties.DnsAddresses) servers.Add(server.ToString());
                if (!string.IsNullOrWhiteSpace(properties.DnsSuffix)) search.Add(properties.DnsSuffix);
                var flags = new List<string>();
                if (device.OperationalStatus == OperationalStatus.Up) flags.Add("up");
                if (device.NetworkInterfaceType == NetworkInterfaceType.Loopback) flags.Add("loopback");
                if (device.SupportsMulticast) flags.Add("multicast");
                interfaces.Add(new NetworkInterfaceInfo(properties.GetIPv4Properties()?.Index ??
                    properties.GetIPv6Properties()?.Index ?? 0, device.Name, properties.GetIPv4Properties()?.Mtu ?? 0,
                    string.Join(":", device.GetPhysicalAddress().GetAddressBytes().Select(x => x.ToString("x2"))),
                    flags, addresses));
            }
            interfaceStatus = new CollectionStatus(partial ? "partial" : "complete", ObservedAt: now);
            resolverStatus = servers.Count == 0
                ? new CollectionStatus("unavailable", "no_device", "no DNS server is configured", now)
                : new CollectionStatus("complete", ObservedAt: now);
        }
        catch (NetworkInformationException error)
        {
            interfaceStatus = new CollectionStatus("unavailable", "no_device", error.Message, now);
            resolverStatus = new CollectionStatus("unavailable", "no_device", error.Message, now);
        }
        token.ThrowIfCancellationRequested();
        return new NetworkSnapshot("windows", now, interfaces, Array.Empty<NetworkRoute>(),
            new NetworkResolver(Distinct(servers), Distinct(search), Array.Empty<string>()),
            interfaceStatus,
            new CollectionStatus("unsupported", "unsupported", "route-table observation is unavailable", now),
            resolverStatus);
    }

    public static string Scope(IPAddress address)
    {
        if (IPAddress.IsLoopback(address)) return "loopback";
        var bytes = address.GetAddressBytes();
        if (address.AddressFamily == AddressFamily.InterNetwork)
        {
            if (bytes.All(x => x == 0)) return "unspecified";
            if (bytes[0] == 169 && bytes[1] == 254 || bytes[0] == 224 && bytes[1] == 0 && bytes[2] == 0) return "link";
            if (bytes[0] == 10 || bytes[0] == 172 && bytes[1] is >= 16 and <= 31 ||
                bytes[0] == 192 && bytes[1] == 168) return "private";
            if (bytes[0] is >= 224 and <= 239) return "multicast";
            return "global";
        }
        if (bytes.All(x => x == 0)) return "unspecified";
        if (address.IsIPv6LinkLocal || bytes[0] == 0xff && (bytes[1] & 0xf) == 2) return "link";
        if ((bytes[0] & 0xfe) == 0xfc) return "private";
        if (address.IsIPv6Multicast) return "multicast";
        return "global";
    }

    /// <summary>Builds a canonical address record from a literal IP and prefix.</summary>
    public static NetworkAddress ParseAddress(string literal, int prefix)
    {
        if (literal.Length == 0 || literal != literal.Trim() || literal.Contains('%') ||
            literal.Contains('[') || literal.Contains(']'))
            throw new ArgumentException("invalid address literal", nameof(literal));
        bool v4 = !literal.Contains(':');
        if (v4)
        {
            var parts = literal.Split('.');
            if (parts.Length != 4 || parts.Any(part => part.Length == 0 || part.Length > 3 ||
                part.Length > 1 && part[0] == '0' || part.Any(ch => ch < '0' || ch > '9')))
                throw new ArgumentException("invalid IPv4 literal", nameof(literal));
        }
        if (!IPAddress.TryParse(literal, out var address) ||
            address.AddressFamily != (v4 ? AddressFamily.InterNetwork : AddressFamily.InterNetworkV6) ||
            prefix < 0 || prefix > (v4 ? 32 : 128))
            throw new ArgumentException("invalid address or prefix", nameof(literal));
        return new NetworkAddress(address.ToString().ToLowerInvariant(), prefix, v4 ? "ipv4" : "ipv6", Scope(address));
    }

    public static IReadOnlyList<string> Distinct(IEnumerable<string> values)
    {
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var result = new List<string>();
        foreach (var value in values)
        {
            var clean = value.Trim(' ', '\t', '\n', '\r', '\v', '\f');
            if (clean.Length > 0 && seen.Add(clean)) result.Add(clean);
        }
        return result;
    }
}
