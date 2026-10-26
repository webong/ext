# clipboard

`github.com/webong/ext/res/system/clipboard` is the reusable, transport-free
system clipboard package inside the system shell module.
It defines bounded multi-format snapshots, opaque generations, and conditional write and clear
operations. The embedding application supplies a policy and native backend.

Generation metadata can be queried without reading clipboard contents, so an application can
grant write-only access while retaining stale-write protection.

The library does not define grants, approval UI, remote request routing, implicit restoration, or
permission onboarding. A native mutation receipt reports dispatch separately from later
verification because operating-system clipboard APIs do not provide a cross-process transaction.

Apple implementations live in `ios/Sources/ExtClipboard`; Android implementations live in
`android/src/android`. They are shipped by `pkg/ext-ios` and `pkg/ext-android` respectively.
