# WebKit browser workspace

`res/web/ios/Sources/ExtWebKit` owns WKWebView behavior. Build independently with
`swift test --package-path res/web/ios --filter ExtWebKitTests`, or use the `ExtWebKit` product of
`pkg/ext-ios`. The SDK links these sources and stages real copies for releases.
This is the native workspace target of the web resource module.

Adapted from POS/Xallet's browser workspace. Expo, app identity, persistent
history, encrypted Passport storage and event routing are not dependencies.
Only the portable `res/web/ios` rules are required, with no plugin engine.

```swift
import ExtWeb
import ExtWebKit

// Main actor; embed page.webView in your app's UI.
let page = BrowserWorkspace(target: "checkout")
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
