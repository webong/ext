# Workspace management and userscript persistence

Swift `ExtWeb.UserscriptStore` and Java `io.github.webong.ext.web.UserscriptStore`
provide portable persistence. Native `ExtWebKit.WorkspaceManagement` and
`io.github.webong.ext.web.android.WorkspaceManagement` route browser-management
requests into an existing `BrowserWorkspace`. The host supplies the workspace,
profile identifier and private store directory; no browser, profile, filesystem
location or external application is selected by the library.

## Store

Each record uses the Go layout:

```
<directory>/<lowercase SHA-256 of UTF-8 target>/<id>.json
```

The existing userscript revision binds source, target, ID, name and match scope.
Loads validate both that revision and the requested identity. Records survive
reopening the store. Lists are sorted by ID. Save distinguishes install from
replace, and `setEnabled` changes the enabled flag without changing the review
revision. Management updates preserve the stored enabled flag.

Use `load`, `list`, `save`, `setEnabled` and `remove` directly for reviewed records,
or the management operations below. `save` never computes a missing revision.
A shared Go fixture verifies the record format and target-directory hash in all
three languages.

New directories use owner-only access and record files use mode 0600. Writes
sync a temporary file and rename it atomically in the same directory. Corrupt,
oversized (over 8 MiB), misidentified and symlink records fail closed; a rejected
update leaves the existing file intact. Mobile readers also reject `.json`
directories rather than skipping them. Temporary files are ignored by listing.

The directory must be app-private and exclusively owned by the host. Operations
are serialized across store instances **within one process**. This is not a
cross-process database or a sandbox against another process swapping parent
directories; hosts sharing a directory across processes must coordinate writers.
The initial root is canonicalized, allowing platform paths such as `/var` on Apple
systems. No CTX configuration directory is used.

## Native router

Swift (on the main actor):

```swift
let store = UserscriptStore(directory: appChosenDirectory)
let router = WorkspaceManagement(workspace: workspace, store: store, profile: profileID)
let request = BrowserManagement.Request(kind: "userscript", action: "list", input: "{}")
let response = try await router.manage(profile: profileID, request: request)
```

Android (UI thread):

```java
UserscriptStore store = new UserscriptStore(new File(context.getFilesDir(), "userscripts"));
WorkspaceManagement router = new WorkspaceManagement(workspace, store, profileID);
router.manage(profileID, request, (response, error) -> {
    // Injection completes asynchronously; handle its execution error here.
});
```

Android throws validation/store failures before returning; asynchronous injection
errors go to the completion callback. Callbacks use the UI thread. Swift reports
both through `throws`. Closing the workspace rejects new router requests and
completes pending injection requests as failures. The router never closes a
host-owned workspace on completion. The host still owns authorization: these APIs
execute caller-approved source and must not be exposed directly to untrusted pages.

Store operations are synchronous. Keep router stores small; applications doing
bulk import or other large disk operations can use the portable store off the UI
thread, then activate the reviewed records on the UI thread. Serialize workflows
that combine several store calls.

| Operation | Mobile behavior |
| --- | --- |
| `userscript.prepare` | Infer metadata, validate and return a source-free description/revision; no write |
| `userscript.install` / `update` | Persist validated inline source; update preserves enabled state |
| `userscript.list` / `describe` | Return descriptions, never script source |
| `userscript.enable` / `disable` / `uninstall` | Change persistence only |
| `userscript.activate` | Replace future-document registrations from the store; optional `id` selects one enabled record |
| `session.targets` / `connect` | Describe/acknowledge the already host-selected workspace |
| `session.navigate` | Issue HTTP(S) navigation; response acknowledges the request, not page-load completion |
| `session.inject` | Evaluate source now in the current main frame, page world; report synchronous evaluation success/failure |
| `session.replay` | Replace future-document registrations with supplied reviewed `records` |

The userscript lifecycle input uses Go's inline fields: `id`, `name`, `source`,
`matches`, `excludeMatches` and optional `revision`. A supplied revision must match
preparation. `sourceFile` is unsupported: the host reads any selected file itself.
`installed` here means the **userscript record** was persisted; it does not mean a
native browser extension was installed.

Session requests can include `target: {"id":"...","protocol":"webview"}`;
userscript requests can include the same object as `sessionTarget`. A supplied ID
must equal the workspace's target and endpoints must be empty. Omission uses the
workspace explicitly selected when constructing the router. The native target
protocol is `webview`; no CDP/BiDi server is created or advertised.

Injection input is `{"source":"...","options":{"runAt":"now","world":"page"}}`.
Omitted timing/world use those defaults. Other timings and worlds are rejected.
JavaScript return values and promises are not part of the response. Android's
native evaluation API exposes no exception callback, so its wrapper evaluates
source and reports a boolean success; environments that forbid this evaluation
fail explicitly. Script error text is not included in the management error.

Replay input is `{"records":[...]}`, using complete `UserscriptRecord` objects so
review revisions can be verified, including target/name bindings. It is a mobile
input schema, not Go's reduced `registrations` schema. Sets are bounded at 512
records and 8 MiB of source. Android activation requires AndroidX WebKit and native
document-start support; absence fails without removing previous registrations.

Activation replaces the complete set of registrations for future documents. Store
mutations do not automatically activate scripts, poll disk, reload pages or undo
JavaScript already executed. Call `userscript.activate` again after an update,
disable or removal, or explicitly replay a new set. Its response states
`lifetime: "workspace"` and `scope: "future-documents"`; closing the workspace clears
registrations. Unsupported extension, bookmarklet and endpoint routes fail
explicitly rather than returning a successful placeholder.

## Checks

- `go test ./...` in `res/web` verifies the baseline record and directory layout.
- `scripts/web-ios.sh` and `scripts/web-android.sh` exercise persistence, corruption,
  links, revision binding, lifecycle operations and input validation.
- `scripts/web-workspace-ios.sh [--simulator <UDID>]` and
  `scripts/web-workspace-android.sh` exercise native activation, injection failures,
  profile binding and close behavior. Android is tested with and without AndroidX.
