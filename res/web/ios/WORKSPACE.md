# WebKit browser workspace

`res/web/ios/Sources/ExtWebKit` owns WKWebView behavior. Build independently with
`swift test --package-path res/web/ios --filter ExtWebKitTests`, or use the `ExtWebKit` product of
`pkg/ext-darwin`. The SDK links these sources and stages real copies for releases.
This is the native workspace target of the web resource module.

Adapted from POS/Xallet's browser workspace. Expo, app identity, persistent
history, encrypted Passport storage and event routing are not dependencies.
Only the portable `res/web/ios` rules are required, with no plugin engine.

```swift
import ExtWeb
import ExtWebKit

// Main actor; embed page.webView in your app's UI.
let page = BrowserWorkspace(target: "checkout")
let capabilities = page.scriptCapabilities
page.onStateChange = { state in /* update UI */ }
page.onVisit = { state in /* optional history persistence */ }
page.onExternalURL = { url in /* app decides whether to launch */ }
try page.replaceUserscripts(reviewedRecords)
try page.navigate("https://example.com")
// back(), forward(), reload(), stop() are also available.
// When the owner ends the session:
page.close()
```

Records must have the selected target, a valid reviewed revision and unique IDs.
Only enabled records are registered. `res/web` generates the page-world replay
source, checking URL matches/exclusions in every frame and preventing duplicate
application per document. Registration runs at document start, before page code.
Replacement affects future documents only; it neither executes immediately nor
undoes code on the current page. Reload explicitly if desired. Invalid updates
preserve the old registrations.

The default WebKit data store is nonpersistent. Pass `dataStore: .default()` for
app-persistent storage or another caller-selected store. Keep the workspace alive
while its view is displayed. It owns delegates and the user-content controller;
do not modify those. All methods and callbacks use the main actor. HTTP(S) and
`about:blank` navigation are allowed; non-web schemes are denied, with clicked
external links offered to the app callback. Nothing opens an external app itself.

`close()` stops loading, drops the document, clears scripts and callbacks. The
host removes the view from its UI. App lifecycle, incoming URLs, storage,
permissions and record retention remain host choices. This is a browser page,
not a sandbox for untrusted applications or the hidden Wasm plugin host.

`scriptCapabilities` describes current-page evaluation separately from future-document
registration. `State.navigationGeneration` changes when navigation begins. Pending current-page
injections fail with `staleNavigation` if that generation changes before native evaluation finishes.

## Attach to an existing WKWebView

`AttachedWebView` runs on the main actor and holds the view weakly. The host
keeps its existing navigation and UI delegates, configuration, and data store.
Forward main-frame navigation starts and content-process termination to
`invalidateNavigation()` from the host delegate. Include same-URL reloads.
This invalidates pending results. JavaScript already submitted to WebKit can
still run.

```swift
let attached = AttachedWebView(webView, target: "checkout",
    exclusiveScriptController: webView.configuration.userContentController)
try attached.replaceUserscripts(reviewedRecords)
try await attached.inject(reviewedSource)
attached.close()
```

Supply a controller only when it belongs exclusively to the selected view.
Omitting it disables future-document scripts. WebKit scopes these scripts to
the controller, so a shared controller would install them in every view using
it. On replacement and close, Ext filters out its own scripts from the
controller's current list and restores the other registrations in order using
WebKit's bulk removal API. Coordinate navigation and controller updates on
the main actor. Removal affects future documents. [WKUserContentController](https://developer.apple.com/documentation/webkit/wkusercontentcontroller)

Cookies use the attached view's `WKWebsiteDataStore`. Views using the same
store see that store's cookies. Close explicitly before releasing the
attachment to remove its registrations. Closing leaves the view available
to the host. Current-page source is limited to 1 MiB, with up to 64 concurrent
evaluations and a 30-second completion deadline. Future scripts use the same
reviewed document-start profile as `BrowserWorkspace`.

See [profiled scripts](../docs/WORKSPACE_MANAGEMENT.md#profiled-scripts-on-attached-views)
for the opt-in `userscriptProfiles` and `replaceProfiledUserscripts` API.

`scripts/web-workspace-ios.sh` tests document-start execution, exclusions,
rejected updates, removal, and closure in real WKWebView on macOS. Sources also
support iOS 13+. To run the same tests on an already booted iOS simulator:

```sh
scripts/web-workspace-ios.sh --simulator <simulator-UDID>
```

The script uses Xcode's `ExtWeb-Package` test scheme and selects `ExtWebKitTests`;
the `ExtWebKit` product scheme has no test action. Both tests passed on an iPhone
16 Pro simulator running iOS 18.3.1. The staged SDK also built for an iOS simulator
consumer against the generated XCFramework archive.

See [mobile cookie workflows](../docs/COOKIE_WORKFLOWS.md) for portable import/export and native workspace cookie access.

`WorkspaceManagement` routes validated session/userscript requests into this workspace and a host-owned `UserscriptStore`; see [management and persistence](../docs/WORKSPACE_MANAGEMENT.md).

Stored scripts can stay synchronized through `activateUserscripts`; the workspace owns
the subscription until close or explicit replacement. See [live reconciliation](../docs/WORKSPACE_MANAGEMENT.md#live-userscript-reconciliation).
