# ext-linux

The Linux platform package: C libraries for the Linux mechanisms ext uses, the
counterpart of [`ext-darwin`](../ext-darwin/README.md),
[`ext-android`](../ext-android/README.md) and
[`ext-windows`](../ext-windows/README.md).

| Directory | Provides |
| --- | --- |
| [`net`](net/README.md) | Local interface, address, route and resolver observation, without opening a remote connection |

The Linux network resource guest in `pkg/ext-linux/net/guest`
compiles `net` for its native read, and its tests run the shared
`tests/fixtures/system/network/v1` cases. Like the other platform packages, these
libraries import no Ext guest or product and choose no policy.

```sh
cmake -S pkg/ext-linux/net -B build/ext-linux-net
cmake --build build/ext-linux-net
ctest --test-dir build/ext-linux-net --output-on-failure
```
