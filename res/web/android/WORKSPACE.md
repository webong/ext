# Android WebView browser workspace

`res/web/android/src/android/java/io/github/webong/ext/web/android` owns Android WebView
behavior. `pkg/ext-android` compiles it into its AAR alongside `res/web/android`.
This is the native source set of the web resource module. Adapted from POS/Xallet's
workspace, with Expo, app identity, persistent history and Passport storage removed.

```java
import io.github.webong.ext.web.android.BrowserWorkspace;

// Android UI thread. Keep page alive while its view is displayed.
BrowserWorkspace page = new BrowserWorkspace(activity, "checkout", new BrowserWorkspace.Listener() {
    @Override public void onStateChange(BrowserWorkspace.State state) { /* app UI */ }
    @Override public void onVisit(BrowserWorkspace.State state) { /* optional history */ }
});
container.addView(page.view());
if (page.supportsDocumentStartScripts()) page.replaceUserscripts(reviewedRecords);
page.navigate("https://example.com");
// back(), forward(), reload(), stop() are also available.
// When the owner ends the session:
page.close();
```

Navigation needs only the Android SDK (API 24+). Userscripts require the consuming
app to add `androidx.webkit:webkit:1.9.0` or newer and the installed WebView to support
`DOCUMENT_START_SCRIPT`. The workspace resolves these optional APIs reflectively;
the SDK supplies R8 consumer rules. AndroidX is not bundled or required for other
SDK features. Unsupported registration throws; there is no late-injection fallback.
See the official [WebViewCompat API](https://developer.android.com/reference/androidx/webkit/WebViewCompat).

`res/web` validates reviewed revisions, target identity, duplicate IDs and scope
before registrations change. Only enabled records run. Replay source checks full
URL matches/exclusions in every frame and prevents per-document duplicate execution.
Replacing/removing registrations affects future documents, not the current page;
reload explicitly if desired. An invalid update preserves old registrations.
An empty set removes registrations even without document-start support.

Methods and callbacks run on the UI thread. The workspace owns the clients; do not
replace them. File and content access are disabled. HTTP(S) and `about:blank` are
allowed; other schemes are blocked. Main-frame requests with a user gesture are
offered to `onExternalURL`, never launched automatically. `close()` removes the view
from its parent and destroys it. Temporary view detachment does not close it.

Android WebView's app-level cookie/storage profile is shared: this is not an
incognito or isolated per-workspace profile. App lifecycle, permission prompts,
incoming intents, storage and record retention remain host choices.

`scripts/web-workspace-android.sh` builds and runs tests on a running device
(`ANDROID_SERIAL` selects it). Set `EXT_ANDROIDX_CLASSPATH` to colon-separated
AndroidX WebKit and runtime dependency JARs. It fetches nothing and needs a JDK,
Android SDK and build-tools, with no NDK or plugin engine. The checks exercise
actual document-start timing, exclusions, rejected updates, removal and closure.
