# Cookie inputs and reliable automation

Cookie storage, profile discovery, credentials, and native scope mapping belong
to browser adapters. CTX supplies portable validation, source selection, and the
bridge between files, processes, and profiles. The public `res/browser` package's `browser.Get` API uses
the same trusted adapters as `ctx share:browser cookie query`.

## Accepted input

Query inline/fallback inputs and `cookie import` accept:

- A version-2 ctx single-cookie bundle from `cookie copy`.
- A ctx query result with a `cookies` array, or a standalone cookie array.
- A Netscape cookie jar with seven tab-separated fields. The subdomain flag,
  Secure flag, expiry, empty values, and `#HttpOnly_` prefix are preserved.
- JSON cookies using `httpOnly`, `hostOnly`, `sameSite`, and `expirationDate`
  or `expires` aliases for the portable fields. Fractional expiry is rounded
  down to seconds; `expires: -1` denotes a session cookie. `hostOnly` determines
  whether the domain allows subdomains. `no_restriction` maps to SameSite `none`.

Inputs must be UTF-8 and at most 8 MiB. Parsing rejects missing values,
conflicting aliases, malformed rows, control characters, and unsupported
fields. Errors identify fields or row numbers without including cookie values.
Native `storeId`, `firstPartyDomain`, and structured `partitionKey` fields need
adapter normalization when populated: the generic parser will not discard
their scope. CTX's `partition_key` and namespaced `attributes` are preserved.
Netscape jars cannot carry SameSite or partition/container scope; use ctx JSON
when that information is needed. See the [curl jar format](https://curl.se/docs/http-cookies.html)
and [JSON cookie field definitions](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/API/cookies/Cookie).

Other Go services can use `browser.ParseCookies(data)` to normalize these inputs
and `browser.BundleCookie(cookie, site)` to produce one validated import bundle.
`InlineCookies.Data`, `JSON`, `Base64`, and `File` are mutually exclusive ways to
supply input. `FallbackInline` uses the same formats.

## Normalize an authorized browser export

`browser.Normalize(ctx, browser.NormalizeOptions{Source: "chrome:Default",
StoreID: "0", Export: payload})` delegates interpretation to an installed,
trusted adapter declaring `cookie.normalize`. The CLI uses the same route:

```sh
ctx share:browser cookie normalize --from chrome:Default --store-id 0 \
  --from-file ./native-export.json --to-file ./normalized-cookies.json

ctx share:browser cookie query --inline-only \
  --inline-file ./normalized-cookies.json --site https://example.com \
  --to-file ./selected-cookies.json
```

Supply one input (`--from-file` or piped `--stdin`) and one output (`--to-file`
or piped `--stdout`). The source and native store ID must be explicit; the CLI
timeout defaults to 30 seconds. Normalization reads only the supplied payload;
it does not discover a profile, read its database, or unlock credentials.

Maintained adapters accept this browser-API envelope (application format/version
metadata may accompany it and is not interpreted by CTX):

```json
{
  "selection": {"browser": "chrome", "profile": "Default", "storeId": "0"},
  "sites": ["https://example.com/account"],
  "names": ["session"],
  "partition": "unpartitioned",
  "cookies": [{
    "name": "session", "value": "synthetic-value", "domain": ".example.com",
    "path": "/", "secure": true, "httpOnly": true, "hostOnly": false,
    "session": true, "sameSite": "lax", "storeId": "0"
  }]
}
```

Persistent records need `session: false` and a finite `expirationDate` in Unix
seconds. All rows must match the selected browser/profile/store, sites, names,
and partition. A partitioned selection uses
`"partition": {"topLevelSite": "https://top.example"}`; cookie rows retain
their browser-native `partitionKey` object. Firefox also accepts an explicit
`firstPartyDomain` selection. Safari rejects unsupported partition/FPI fields.
Sites are limited to 16, names to 32, and the complete adapter request to 8 MiB.
Any invalid row fails the whole normalization and creates no output file.

The result is canonical JSON with `source` and `source_info.store_id`.
Adapters retain the opaque native store ID, partition metadata, first-party
isolation, and bounded unknown cookie fields in namespaced attributes. They do
not assume that a store ID means the default profile or map it to a disk
container. Those attributes remain part of merge identity, so an API export and
a disk read may represent distinct scopes. Use `InlineOnly`/`--inline-only` for
an explicitly selected export when combining sources is unnecessary. Header,
Netscape, and native import paths that cannot preserve these attributes reject
the records; retain JSON.

The exporting application must obtain the browser/profile/store binding from
its trusted runtime and handle permissions and user approval. A file's asserted
selection cannot prove which runtime produced it. CTX checks consistency with
the endpoint supplied by that application.

## Output for HTTP tools

JSON is the default and retains native scope and source metadata. Query also
supports `--format header` for one request URL and `--format netscape` for curl
cookie jars. The same conversions are available in Go as
`browser.CookieHeader(cookies, site)` and `browser.NetscapeCookies(cookies)`.

```sh
ctx share:browser cookie query --from chrome:Default \
  --site https://example.com/account --strict --require-match \
  --format header --to-file ./cookie-header.txt

ctx share:browser cookie query --from firefox:personal \
  --site https://example.com --strict --require-match \
  --format netscape --to-file ./cookies.txt

curl --cookie ./cookies.txt https://example.com/
```

Header output contains `Cookie: name=value; ...`. It requires exactly one site,
excludes expired cookies, and puts longer cookie paths first while retaining
duplicate names from different paths. Values that cannot be represented in an
HTTP cookie header cause an error; CTX does not rewrite them. An empty result
produces a blank line unless `--require-match` is used.

Netscape output preserves domain/subdomain scope, path, Secure, HttpOnly, expiry,
and empty values. The jar format cannot retain SameSite or source metadata.
Both text formats reject partitioned cookies and cookies with adapter-owned
attributes because they cannot preserve native isolation. Use CTX JSON for
those records. Conversion failure creates no output file. All formats retain
the private-file and pipe output rules.

## Import a selected cookie

```sh
ctx share:browser cookie import --from-file ./site-cookies.json \
  --site https://example.com --name session --to-profile chrome:Default

ctx share:browser cookie import --from-file ./cookies.txt \
  --site https://example.com --name session --domain .example.com \
  --path /account --to-profile chromium:Default
```

A ctx bundle supplies its site. Other inputs require `--site`. Import selects
exactly one active cookie; ambiguous matches require `--domain`, `--path`, or
namespaced `--attribute key=value` filters. It preserves the source label when
available, validates secure prefixes and SameSite, and sends a version-2 bundle
to the target adapter. This does not perform a bulk import. An input query's
warnings are reported before importing the selected cookie.

The destination must declare `cookie.import` and be able to preserve the scope.
Maintained Chromium import supports macOS/Linux closed profiles; Firefox import
supports unpartitioned persistent cookies in closed profiles. A session cookie
cannot be imported into Firefox's persistent store without changing its
lifetime, so its adapter rejects it. Safari and Windows Chromium profile import
remain unavailable. `--replace` is required to overwrite an existing cookie.

## Query completion and precedence

```sh
ctx share:browser cookie query --from firefox:personal \
  --site https://example.com --name session --strict --require-match \
  --timeout 30s --to-file ./site-cookies.json
```

Without `--strict`, a query returns available cookies and warnings for sources
or rows it could not read. `--strict` fails on any warning, even when fallback
input provides a missing cookie. `--require-match` fails when no cookies match.
Both failures leave the output file uncreated. Cancellation and overall timeout
are errors; they do not emit a partial CLI result. Library callers can inspect
partial results returned alongside a cancellation error. Cancellation terminates
the adapter invocation and its helper processes, with bounded pipe cleanup. Empty successful
queries return `{"cookies":[]}`.

Merge mode retains the first value for each native cookie scope; it does not
decide which login is freshest. Inline input takes precedence, then ordered
browser sources, then fallback input fills missing scopes. First mode stops at
the first source yielding any match, and uses fallback only if all earlier
sources yielded none. Select explicit profiles when session provenance matters.

## Platform boundaries and validation

Browser-API cookie export, permission requests, user interaction, and export
delivery belong to the exporting application or extension. CTX consumes the
supplied payload through its portable inline/fallback APIs. Native store, container,
and partition fields that require browser-specific interpretation are
normalized by the owning CTX adapter before entering the portable pipeline.

Windows Chromium App-Bound `v20` cookies cannot be decrypted by the maintained
standalone adapter. Safari reads its accessible disk cookie store and can miss
normal-session cookies still in memory. An authorized export can supply such
values through inline/fallback input; CTX does not obtain that export itself.
OS credential access and recently uncommitted browser state still affect reads.

Chromium adapters cache a credential lookup and derived key for one cookie
operation, including a failed lookup, so a query does not repeat a denied
Keychain or wallet prompt for every matching row. The cache is discarded with
the request. Separate requests attempt credential access again.

Linux v11 credentials use `secret-tool` or `kwallet-query`. Secret Service tries
the adapter's application identity, then its service/account identity. KWallet
uses `CTX_KWALLET_NAME` when set; otherwise `dbus-send` discovers the network
wallet from KDE 6, KDE 5, or the older service, with bounded timeouts and a
`kdewallet` fallback. Browser credentials and wallet conventions stay in the
Chromium adapter engine. These are external helper backends; no direct Go
keyring dependency is required.

Regression fixtures cover source order/trust, filtering, merge/fallback behavior,
partial failures, cancellation, input parsing, and CLI import selection. Native
SQLite fixtures exercise Firefox expiry schemas 15–17, container/partition scope
preservation, and encrypted Chromium import/read/replace with domain binding and
profile locks. Safari fixtures exercise disk reads, stale references, and malformed
records. These checks use synthetic data and do not access a personal
browser profile. Cross compilation confirms build compatibility; real Windows
DPAPI, macOS Keychain/TCC, Linux wallet integrations, and Safari live-session
freshness still require validation on those operating systems and browsers.

### Synthetic credential checks

Ordinary tests use private files and mocked credential helpers. They cover
network-wallet discovery, explicit wallet precedence, Secret Service identities,
helper failures, and request-local key/error caching. Safari tests use synthetic
binary stores; they do not establish access to Safari's real container or its
in-memory cookies.

Additional opt-in tests exercise the real OS credential API with dummy data:

```sh
# Run on macOS or Windows; PowerShell users set $env:CTX_COOKIE_NATIVE_CREDENTIAL_TESTS = '1'.
CTX_COOKIE_NATIVE_CREDENTIAL_TESTS=1 go test -v ./adapters/chromium/engine \
  -run '^TestNativeCookieCredentials' -count=1
```

The macOS test creates and deletes an isolated temporary keychain containing
only a synthetic Safe Storage item. It passes that keychain explicitly and
does not read any browser item. Windows tests protect dummy data for the
current user, then exercise legacy DPAPI and a DPAPI-wrapped Chromium AES-GCM
key; they retain the App-Bound rejection. Native CI enables these checks on
hosted macOS and Windows runners. Adding a CI job is not evidence that it has
passed; review the job result after the branch is pushed.

Recorded on 2026-10-01: the isolated macOS Keychain fixture passed locally.
The full local race suite, vet, native CLI integration tests, and Windows/Linux
all-package builds passed. The Windows credential test compiled but has not
executed here. Browser-backed Keychain access, Windows DPAPI execution, actual
Linux wallet sessions, and Safari freshness are still separate checks.

### Later browser validation

On macOS, use a clean user/profile as the browser validation environment.
Start with a disposable profile and a local test site that sets
known persistent, session, HttpOnly, Secure, and path-specific cookies. Verify
the actual adapter reads and protected outputs against those known values,
then verify denied credential access and cancellation. Real browser Keychain
access controls still need this check even after the isolated-keychain test.

For Safari, compare the browser's current cookies with the readable disk store
after setting cookies and restarting Safari; record which session cookies never
reach disk and whether TCC blocks the read. Use an authorized browser export
for values missing from disk. Synthetic parsing checks cannot establish live
session freshness.

Hosted Windows CI can validate DPAPI without owning a Windows laptop. A later
Windows VM or hosted interactive desktop is needed for browser profile locks
and App-Bound/authorized-export behavior. Hosted Linux CI covers portable and
mocked-helper tests; a Linux desktop VM on a Mac can validate actual GNOME
Secret Service and KDE KWallet prompts with a disposable browser profile. ARM
Linux is suitable for those workflows; it does not establish x86 compatibility
by itself. No personal cookies need to be copied between machines.

Native SQLite operations send SQL through stdin so imported cookie values do
not appear in process arguments. Write failures omit SQL diagnostics that could
repeat a value. Cookie output files are created exclusively with mode 0600;
stdout output requires a pipe.
