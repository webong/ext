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
WebViewCapabilities capabilities = page.scriptCapabilities();
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

`scriptCapabilities()` reports current-page evaluation independently from optional AndroidX
future-document registration. `State.navigationGeneration` changes when navigation begins, and a
pending current-page injection completes unsuccessfully if the generation changes.

Android WebView's app-level cookie/storage profile is shared: this is not an
incognito or isolated per-workspace profile. App lifecycle, permission prompts,
incoming intents, storage and record retention remain host choices.

`scripts/web-workspace-android.sh` builds and runs tests on a running device
(`ANDROID_SERIAL` selects it). Set `EXT_ANDROIDX_CLASSPATH` to colon-separated
AndroidX WebKit and runtime dependency AARs (or JARs without minification). It fetches nothing and needs a JDK,
Android SDK and build-tools, with no NDK or plugin engine. The checks exercise
actual document-start timing, exclusions, rejected updates, removal and closure.

To verify the packaged SDK and its reflection keep rules, pass an AAR and enable
R8. Supply dependency AARs so their own consumer rules are included alongside
the SDK rules, as they would be by Gradle. The script does not resolve or download
dependencies.

```sh
scripts/ext-android-release.sh 0.0.0 # local verification artifact; publishes nothing
EXT_ANDROIDX_CLASSPATH=/path/webkit.aar:/path/core.aar \
  scripts/web-workspace-android.sh \
  --aar dist/ext-android-0.0.0/ext-android-0.0.0.aar --minify
```

`--build-only /path/test.apk` produces the signed test app without touching a
device, useful for build verification. Runtime verification still requires
installing and executing that app. The test APK is for verification only.

Use `--without-androidx` to verify the optional dependency boundary: navigation
still works, document-start support is false, nonempty userscript registration
throws, and clearing registrations succeeds. This mode needs no AndroidX dependencies
and can also run against the packaged AAR with `--minify`.

Verified on an Android 14 (API 34) arm64 emulator using the generated SDK AAR:
all eight document-start/lifecycle checks passed with R8 and AndroidX WebKit
1.9.0, and all four checks passed with R8 and no AndroidX dependency.

See [mobile cookie workflows](../COOKIE_WORKFLOWS.md) for portable import/export and native workspace cookie access.

`WorkspaceManagement` routes validated session/userscript requests into this workspace and a host-owned `UserscriptStore`; see [management and persistence](../WORKSPACE_MANAGEMENT.md).

Stored scripts can stay synchronized through `activateUserscripts`; the workspace owns
the subscription until close or explicit replacement. See [live reconciliation](../WORKSPACE_MANAGEMENT.md#live-userscript-reconciliation).
