# Linux network state

This C library observes local interfaces, assigned addresses, routes and
resolver configuration without opening a remote connection. It uses
`getifaddrs`, sysfs, `/proc/net/route`, `/proc/net/ipv6_route` and
`/etc/resolv.conf`. All collection arrays and time are bounded by the
caller's options. Missing native sources are reported in the corresponding
collection state.

Build and test on Linux with CMake and CTest. The tests run the shared
`tests/fixtures/system/network/v1` option and address cases as well as live
bounded observation. The API is independent of Go and of Ext guests; a
resource guest can expose this library through Ext.
