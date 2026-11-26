# Native networking

`lib/system/net` contains native observation of the OS network state: local
interfaces, assigned addresses, routes and resolvers. Its Swift, Java, .NET and Linux C
implementations are reusable by applications and by Ext resource guests.
They do not open a remote connection or choose a network policy.

The Go contract types remain at `res/system/service/network`; native collection
is performed by these libraries and their Ext resource guests.
Windows and Linux each have an `ext.system.network/v1` resource guest under
`res/system/service/network`; the Linux guest uses Go for plugin transport
while compiling this C library for the native read.
The Windows .NET library reports route observation as unsupported.
