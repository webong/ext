# system/network

`github.com/webong/ext/res/system/network` observes networking supplied by the local
operating system: interfaces, assigned addresses, route tables, and resolver
configuration. It opens no sockets, performs no lookup, sends no traffic, and
does not mutate network configuration.

Each collection has its own status so an empty result is distinguishable from
an unsupported, inaccessible, partial, or unavailable OS source. Linux route
tables are read from procfs. Interface observation is portable; platforms that
do not yet expose route or resolver configuration report that explicitly.

On macOS the Go and Swift bindings read the kernel routing MIB and public
SystemConfiguration DNS state directly, without subprocesses. Resolver status
is partial because this state omits supplemental resolver policy; Go builds
with cgo disabled explicitly report DNS observation as unsupported. iOS route
and resolver observation remains unsupported.

The Apple and Android bindings live under `ios/` and `android/` and ship as the
`ExtNetwork` and `io.github.webong.ext.network` products in the native SDKs.
They remain local observers. Connection clients, tunnels, remote discovery,
service routing, credentials, and authorization belong to their consumers.
