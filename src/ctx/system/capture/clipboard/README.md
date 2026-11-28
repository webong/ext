# clipboard

`github.com/webong/ext/src/ctx/system/capture/clipboard` is the reusable, transport-free
system clipboard package inside the system shell module.
It defines bounded multi-format snapshots, opaque generations, and conditional write and clear
operations. The embedding application supplies a policy and native backend.

Generation metadata can be queried without reading clipboard contents, so an application can
grant write-only access while retaining stale-write protection.

The library does not define grants, approval UI, remote request routing, implicit restoration, or
permission onboarding. A native mutation receipt reports dispatch separately from later
verification because operating-system clipboard APIs do not provide a cross-process transaction.

The native Apple, Android and .NET implementations live in
`act/clipboard` of [`pkg/ext-darwin`](../../../../../pkg/ext-darwin/act/clipboard), [`pkg/ext-android`](../../../../../pkg/ext-android/act/clipboard/README.md) and [`pkg/ext-windows`](../../../../../pkg/ext-windows/act/clipboard/README.md).
This Go package retains the portable contract. On Windows, CTX invokes the
installed `system.clipboard.windows` Ext resource guest; it does not implement
Win32 clipboard calls in Go.
