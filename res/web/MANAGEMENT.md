# Mobile browser-management protocol

`BrowserManagement` in Swift `ExtWeb` and Java `io.github.webong.ext.web` ports
`contract.Request`, `Response`, the operation vocabulary, statuses, and endpoint
validation. All code lives in `res/web`; the existing platform SDK source links
ship it without an extra dependency.

Requests carry `version`, `kind`, `action`, and optional object `input`. Responses
carry matching identity fields, a nonempty status of at most 64 UTF-8 bytes, and
optional JSON `result`. The version is `1.0`. Unknown statuses are accepted;
`persistent` returns true only for `installed`. Extension activation cannot report
`installed`, and extension installation cannot report `activated`.

The operation vocabulary covers extension, userscript, session and bookmarklet
management. This vocabulary is **not a capability declaration**. A host still
supplies the backend and decides which operations it supports. The protocol port
does not install browser extensions, discover desktop profiles, or create a
CDP/BiDi server in a native webview.

## Using the contract

Swift:

```swift
let request = BrowserManagement.Request(kind: "userscript", action: "list", input: "{}")
let wireRequest = try request.json()
let response = try BrowserManagement.dispatch(profile: profileID, request: request) {
    profile, request in
    try backend.manage(profile: profile, request: request)
}
let wireResponse = try response.json(for: request)
```

Swift also provides an async dispatch overload for native workspace callbacks.
The backend owns actor/thread affinity and cancellation. Java uses a synchronous,
throwing functional interface; Android hosts must schedule native work on the
appropriate thread and must not block the UI thread waiting for callbacks.

Java:

```java
BrowserManagement.Request request = BrowserManagement.Request.fromJson(wireRequest);
BrowserManagement.Response response = BrowserManagement.dispatch(profileID, request,
    (profile, operation) -> backend.manage(profile, operation));
String wireResponse = response.toJson(request);
```

Dispatch validates before invoking the backend and validates its response before
returning it. Backend failures propagate to the caller. Unsupported operations
must fail in the backend; dispatch does not silently select a browser or infer
success. Backends can use the existing `WorkspaceScripts`, userscript records,
bookmarklet helpers and native workspaces to implement their declared routes.

## Payloads, limits and endpoint safety

Input/result are raw JSON strings. `nil`/Java `null` means absent; the string
`"null"` is an explicit JSON null (valid for results, invalid for input).
Payloads retain their number precision, whitespace and member order. Parsing
rejects trailing data and invalid JSON number syntax. Envelope fields follow
Go's case-insensitive field matching, ignore unknown fields and use the last
occurrence of a recognised field.

Input is bounded at 8 MiB, results at 16 MiB and encoded responses at 16 MiB plus
64 KiB. Mobile request decoding permits 8 MiB plus 64 KiB of envelope space;
Go's CLI reader has a separate 8 MiB total input bound. Java rejects malformed UTF-16 in raw JSON strings before encoding. Mobile JSON readers cap
nesting at 128 containers (Go allows deeper input). They accept Unicode strings;
Swift's decoding of envelope strings rejects malformed surrogate escapes rather
than replacing them as Go/Java do. Transport readers must enforce byte bounds
before buffering untrusted messages; these APIs validate already-buffered strings.

`Endpoint` accepts only `cdp`/`bidi`, `ws`/`wss`, loopback literals or `localhost`,
and an explicit numeric port. It rejects credentials, queries, fragments and
unknown endpoint fields. It does not resolve DNS or impose a port range absent
from the Go contract. Endpoint URLs are bounded at 2048 bytes, endpoint result
JSON at 4096 bytes. URL paths are capabilities: request, response and endpoint
descriptions are redacted, while explicit JSON serialization retains the secret.
Do not log wire JSON or backend errors that include payloads.

## Verification

`testdata/management/v1/protocol.json` is consumed by Go, Swift and Java. It checks
the operation vocabulary, wire envelopes, statuses, install/activation semantics,
raw JSON and loopback endpoint constraints against the Go baseline. Platform
unit tests additionally cover dispatch ordering, backend failures, payload byte
limits, nesting limits and diagnostic redaction.

Run `go test ./...` from `res/web`, `scripts/web-ios.sh`, and
`scripts/web-android.sh` from the repository root.

[Workspace management and persistence](WORKSPACE_MANAGEMENT.md) implements mobile
session and userscript routes over the existing native workspaces and a host-owned
store. It documents the supported input schemas and activation lifetime.
