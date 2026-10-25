# Mobile cookie workflows

The portable `CookieWorkflows` API lives in `res/web/ios/Sources/ExtWeb` and
`res/web/android/src/main`. It has no native WebView dependency. The SDKs ship
these sources through their existing source links/source sets.

- `parse` accepts UTF-8 bytes (at most 8 MiB and 128 JSON nesting levels): a cookie JSON array, a query's
  `cookies` array, a version-2 single-cookie bundle, or a Netscape jar. Common
  export aliases (`httpOnly`, `hostOnly`, `sameSite`, `expirationDate`, `expires`,
  `session`) are normalized with conflict checks. Unknown cookie fields and
  uninterpreted native store/partition aliases are rejected.
- The result contains portable `Cookie` fields, including partition and adapter
  attributes. Query provenance (`source` / `source_info`) is not returned; keep
  that separately if needed. This API does not discover or merge browser sources,
  read desktop profiles, or interpret adapter-specific exports.
- `header` filters by URL and expiry, orders longer paths first, and preserves
  duplicate names at different paths. It rejects values that cannot be represented
  in a request header and matching cookies with native isolation metadata.
- `netscape` exports a curl-compatible jar. It preserves HttpOnly and session versus
  persistent expiry, but the format loses SameSite and provenance. Isolation
  metadata is rejected rather than silently discarded.
- `bundle` validates one cookie for a selected request URL and strips its query
  and fragment. Existing `Cookie` / `CookieBundle` JSON APIs provide JSON export.

All successful exports and native read results contain credentials. Errors from
parsing and conversion do not include values. The shared workflow fixture file is
`testdata/cookie/v2/workflows.json`, run by Go, Swift and Java tests.

## Swift

```swift
import ExtWeb
import ExtWebKit

let cookies = try CookieWorkflows.parse(data)
let header = try CookieWorkflows.header(cookies, site: "https://example.com/account")
let jar = try CookieWorkflows.netscape(cookies)
let bundle = try CookieWorkflows.bundle(cookies[0], site: "https://example.com/account")
try workspace.setCookie(bundle) {
    // WebKit finished the write. Navigate after this callback if needed.
}
try workspace.cookies(for: bundle.site) { nativeCookies in
    // [HTTPCookie], including HttpOnly records, filtered by URL and expiry.
    // These are WebKit's native records, not a lossless portable export.
}
// Delete one native record returned by the read:
try workspace.deleteCookie(nativeCookie) { }
```

These methods use the workspace's `WKWebsiteDataStore.httpCookieStore`, not
`HTTPCookieStorage.shared` or Safari's cookies. A workspace uses a nonpersistent
store by default; callers may supply a website data store. Reads expose only
metadata available through `HTTPCookie`; they do not manufacture partition or
source metadata. The write conversion verifies that WebKit's cookie object
preserves the requested identity, flags, SameSite and expiry.

## Android

```java
List<Cookie> cookies = CookieWorkflows.parse(bytes);
String header = CookieWorkflows.header(cookies, "https://example.com/account");
String jar = CookieWorkflows.netscape(cookies);
CookieBundle bundle = CookieWorkflows.bundle(cookies.get(0), "https://example.com/account", "inline");
workspace.setCookie(bundle, accepted -> {
    // Boolean native write result. Navigate here when accepted is true.
});
String nativeHeader = workspace.cookieHeader(bundle.site);
```

Android's `CookieManager` exposes URL-scoped request headers, including HttpOnly
cookies, but not complete cookie records. `cookieHeader` therefore returns the
header directly, or an empty string when absent; it does not invent domain, path,
expiry, or SameSite fields. Partitioned cookies, if present in the store, are
returned according to CookieManager's top-level URL partition behavior. Do not
use this header as a lossless cookie export.

The app-level WebView cookie store is shared across workspaces. These methods do
not change acceptance/third-party-cookie policy, force persistence, or clear the
store. AndroidX is not required for cookie access.

## Native write boundaries

Writes on both platforms accept a validated, active `CookieBundle` and reject
partition keys, cross-site-ancestor flags and adapter attributes that these APIs
cannot faithfully import. They also reject non-ASCII native names/domains and
unsafe header values or paths, rather than allowing attribute injection or silent
normalization. Domain cookies use an explicit Domain attribute; host-only cookies
omit it. All validation happens before the single-cookie mutation.

Calls and callbacks run on the UI thread/main actor. New calls after workspace
closure fail; an already accepted asynchronous read/write may still complete.
Completion reports the native operation, not authentication success or a guarantee
that a server will accept the cookie. Multi-cookie imports are separate writes,
not transactions.

Run `scripts/web-ios.sh`, `scripts/web-android.sh`, and `go test ./...` in
`res/web` for portable fixtures. `scripts/web-workspace-ios.sh` (or its
`--simulator <UDID>` mode) and `scripts/web-workspace-android.sh` exercise actual
native cookie stores, including rejected writes and URL isolation. Android's
`--without-androidx` mode also exercises cookies.

Verification on 2026-10-25: all 36 workflow fixtures passed in Go, Swift and Java;
Go tests/vet passed; Java's full suite passed 436 checks. All three native WebKit
tests passed on macOS and the iOS 18.3.1 iPhone 16 Pro simulator. The Android 14
arm64 emulator passed 17 checks with AndroidX WebKit 1.9.0 and 13 without AndroidX,
both against the built SDK AAR with R8 enabled. Physical devices were not tested.
The iOS navigation test attaches its view to a window to avoid WebKit suspension;
the Android test ignores callbacks after its Activity is destroyed/recreated.

See [mobile cookie queries](COOKIE_QUERY.md) for host-authorized source ordering, merge/first modes, fallback, provenance and cancellation.
