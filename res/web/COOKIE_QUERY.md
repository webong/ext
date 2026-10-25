# Mobile cookie query orchestration

Swift `ExtWeb.CookieQuery` and Java `io.github.webong.ext.web.CookieQuery` port
Go's `Get` workflow. All implementation code is in `res/web`. The host supplies
an authorized backend; the library does not choose browsers, inspect another
app's storage, grant permissions, or depend on CTX/plugin adapters.

## Ordering and scope

1. Validate options and parse both inline and fallback input.
2. Filter inline cookies first.
3. Ask the backend for an ordered source selection and discovery warnings.
4. Query each source for each requested site, filter its results, and merge them.
5. Fill missing identities with fallback inline cookies.

`mode = "merge"` (also the empty default) combines all sources. `"first"` stops
with matching inline cookies, or after the first source that contributes cookies
across its requested sites. Fallback is used only if no earlier source matches.
`inlineOnly` skips the backend entirely. Invalid fallback input fails even if an
earlier source would win. Both inputs accept the existing bounded JSON/Netscape
formats (8 MiB each); hosts read selected files or decode base64 to bytes first.

A URL/origin is required unless `allowAllHosts` is explicitly true. Filtering
uses Go's domain, escaped-path, Secure, expiry and exact-name rules. Empty URL
paths match every cookie path on that host. Source requests include a parsed
site, names, include-expired, and an all-host flag when the site is absent.

Identity includes name, domain, path, partition key, cross-site-ancestor flag,
and the complete opaque attribute map. Attribute order does not matter; UTF-8
string differences do. The first value for an identity wins. Distinct native
scopes remain distinct; ID/ref, value, expiry and Secure do not change merge
identity, matching Go.

## Host backend

The backend receives the selection options (`sources`, `browsers`, `profiles`,
`preferredSource`) and owns their interpretation, authorization and final order.
Explicit sources and browsers are mutually exclusive. Inline-only queries reject
both. The workflow preserves the returned order; it does not reorder by adapter
name or provide browser-specific fallbacks.

```swift
var options = CookieQuery.Options()
options.url = "https://example.com/account"
options.mode = "merge"
options.timeout = 10
let result = try await CookieQuery.get(options) { context, selection in
    try context.check()
    // Authorize/select sources here using selection.
    return .init(sources: [
        .init(adapter: "app", profile: "work", label: "app:work") { context, request in
            try context.check()
            let cookies = try await authorizedStore.query(request)
            return .init(cookies: cookies, warnings: [], storePath: "")
        }
    ])
}
```

```java
CookieQuery.Options options = new CookieQuery.Options();
options.url = "https://example.com/account";
options.timeoutMillis = 10_000;
CookieQuery.Cancellation cancellation = new CookieQuery.Cancellation();
// Call on a worker thread; backend code dispatches native operations as required.
CookieQuery.Result result = CookieQuery.get(options, (context, selection) -> {
    context.check();
    return new CookieQuery.Selection(Collections.singletonList(
        new CookieQuery.Source("app", "work", "app:work", (ctx, request) -> {
            ctx.check();
            return new CookieQuery.Batch(authorizedStore.query(request));
        })));
}, cancellation);
```

Source callbacks return **typed cookies**, not raw adapter protocol frames.
Backends that speak Go's adapter protocol must decode/bound their response before
returning a batch. Sources can alternatively provide `list` and `export` callbacks
for legacy stores. Legacy sources cannot query all hosts or include expired
cookies; unsupported requests produce a warning without calling the store.
Exports must match the listing's ID/ref and complete scope; substituted cookies
are rejected. Invalid batch rows are skipped with warnings.

## Results, failures and lifetime

Each result entry contains `cookie`, `source` and `sourceInfo` (adapter, profile,
store path, inline/fallback flags). Inline provenance is assigned by this query;
input provenance cannot impersonate a backend source. To feed results into an
existing export helper, explicitly select each entry's `cookie`.

Source and export failures become warnings so other sources and fallback can
still succeed. Source labels prefix warnings. Exceptions are replaced with
credential-free generic messages; backend-supplied warning strings and labels
must already be safe to display. Default entry/result descriptions omit values.
Discovery failures and invalid source descriptors are fatal. Unlike Go's tuple
return, `Failure.partial` carries accumulated cookies/warnings on fatal backend,
cancellation or timeout errors. Validation/parsing errors propagate without
source work.

Deadlines use monotonic clocks. Cancellation/deadlines are checked before and
after discovery, source queries and legacy exports. They are **cooperative**:
backends must honor the supplied context/deadline during long operations; the
library cannot forcibly stop a blocked native operation. Swift also checks task
cancellation; Java supports a cancellation token and thread interruption. Java
snapshots options and passes a separate snapshot to discovery. Callers must not
mutate Java options concurrently while starting a query.

## Native cookie access

Existing workspace cookie APIs remain available. A backend can bridge those
APIs where they provide the metadata required by the cookie model, using their
UI-thread/main-actor requirements. Android `CookieManager` exposes only a
URL-scoped header, not full cookie records; it is **not** automatically converted
into query rows with invented domain/path/expiry metadata. Store normalization
and native-export provenance binding (`Normalize` in Go) remain separate work.

## Checks

`testdata/cookie/query.json` runs against Go, Swift and Java and covers ordering,
first-source short circuiting, inline-only/fallback, filters, expiry, all-host and
multi-site queries, partition attributes and UTF-8 identity. Platform tests also
check failures, legacy binding, cancellation/deadlines and redacted diagnostics.

Run Go tests in `res/web`, `scripts/web-ios.sh`, and `scripts/web-android.sh`.
The Swift query tests also run on the iOS simulator; Android's native workspace
harness runs the same Java behavior checks on device, including against a
minified SDK AAR.
