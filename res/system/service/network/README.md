# system/network

`github.com/webong/ext/res/system/service/network` defines the bounded
`ext.system.network/v1` snapshot, options, status and address vocabulary.
It validates these records but performs no operating-system observation.

Each collection has its own status so an empty result is distinguishable from
an unsupported, inaccessible, partial, or unavailable OS source.

The Apple and Android native libraries live in `lib/system/net/ios` and
`lib/system/net/android`; Windows uses `lib/system/net/windows` and Linux
uses `lib/system/net/linux`. Ext resource guests for Windows and Linux are
under `service/network/windows/Guest` and `service/network/linux/guest`.
The Go contract types remain available; `Discover` and the platform-specific
Go collectors have been removed. Connection clients, tunnels, remote discovery,
service routing, credentials, and authorization belong to their consumers.
