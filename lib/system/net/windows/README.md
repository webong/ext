# Windows network library

`Ext.Network.Windows` observes local interfaces, assigned addresses, DNS servers
and the configured DNS suffix through .NET's operating-system network APIs. It
performs no DNS lookup or remote connection. The route-table status is
`unsupported`, matching the current Go Windows API. Record bounds and cancellation
are caller supplied. The library can be used directly by a .NET application.
The private Ext resource guest at
`res/system/service/network/windows/Guest` exposes it to other languages
through `ext.system.network/v1`.

Run contract tests with `dotnet run --project lib/system/net/windows/Tests`.
