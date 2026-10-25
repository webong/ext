# Local web bundles

Go `bundle.Run`, Swift `ExtWebKit.BundleRunner`, and Java
`io.github.webong.ext.web.android.BundleRunner` run an immutable directory of web
content. All implementations live in `res/web`; the mobile SDK packages ship
these sources. They do not depend on the plugin engine, Expo, or product adapters.

The mobile runner owns a WebView and an ephemeral IPv4 loopback HTTP origin.
Attach the view before starting the run. The shared `bundle/shim.js` is injected
before page scripts and uses its HTTP transport (`ext.host.transport === "http"`).
`scripts/plugin-mobile-assets.sh` generates its mobile copies; `--check` catches drift.

```swift
import ExtWebKit

var options = BundleRunner.Options(root: directoryURL)
options.entry = "main.mjs" // index.html by default
options.timeout = 60
let runner = try BundleRunner(options: options)
// Attach runner.webView to the application's view hierarchy.
runner.onConsole = { print($0.level, $0.text) }
runner.onMessage = { print("page:", $0) }
runner.onReady = { try? runner.send("hello") }
let result = try await runner.run() // main actor; cancellation closes the run
```

```java
BundleRunner.Options options = new BundleRunner.Options(directory);
options.entry = "main.mjs";
options.timeoutMillis = 60_000;
BundleRunner runner = new BundleRunner(activity, options, new BundleRunner.Listener() {
    @Override public void onConsole(String level, String text) { /* render log */ }
    @Override public void onMessage(String text) { /* receive page message */ }
});
activity.setContentView(runner.view());
runner.run().thenAccept(result -> { /* inspect status, exitCode, reason, url */ });
// runner.send("hello") can be called from any thread; it queues until the page polls.
// Close the runner when its Activity is destroyed; never block the UI on the future.
```

Page API:

```js
console.log("ready");
ext.host.onmessage = message => { ext.host.send("received: " + message); };
ext.exit(0);
```

## Behavior

- HTML entries get the shim before their first script. `.js` and `.mjs` entries
  get a generated module page. Relative modules, fetches, workers and WASM use
  the local origin.
- The default CSP permits local resources, inline scripts and WASM compilation,
  with external connections blocked. `BundlePolicy` can allow specific origins
  or all network connections; shared Go/Swift/Java fixtures verify its output.
- Only regular files beneath the canonical root are served. Dotfiles, parent
  traversal, NUL paths and symlinks escaping the root are refused. Internal
  symlinks are allowed. Keep the directory unchanged during a run: path checks
  do not protect against a concurrent writer swapping files or symlinks.
- Every run uses a random control token, exact Host validation, same-origin
  control requests and no-store/nosniff/no-referrer headers. The mobile views
  refuse navigation outside their loopback origin and do not expose native JS
  interfaces. CSP is a resource policy, not an untrusted-code sandbox.
- Console levels are log/info/warn/error/debug/exception, capped at 64 KiB.
  Host messages are UTF-8 strings capped at 24 MiB, with 256 queued messages.
  Files are capped at 64 MiB. Control bodies are capped at 256 KiB except host
  messages, whose encoded-body cap is 48 MiB + 1 KiB, matching Go.
- Readiness fires once on the first page report. Default startup timeout is 30s,
  heartbeat timeout 15s; zero overall timeout means unlimited. Mobile callbacks
  run on the UI thread. Background suspension can cause heartbeat closure.
- Results are `exited`, `closed`, `timeout`, or `failed`, with the first outcome
  winning. Explicit close/cancellation releases the origin and executing page.
  Runners are single-use; retain them until completion and close on host teardown.

## Platform integration and limits

Android requires `INTERNET` permission and cleartext HTTP permission for
`127.0.0.1`. Use an application network-security configuration that permits only
that domain; the SDK does not change application network policy. The test harness
permits cleartext for its disposable test application. No AndroidX dependency is
needed. Java construction, `view()` and `run()` use the UI thread; `send()` and
`close()` are thread-safe. Canceling the returned future closes the runner.
Android WebView cookies and storage remain app-wide, as with browser workspaces;
the changing port is not cookie isolation.

Swift uses a nonpersistent WKWebsiteDataStore. Applications with restrictive ATS
settings must allow local networking. Construction, view access, callbacks and
`run()` use the main actor. It uses Apple's Network framework for loopback IO.

`crossOriginIsolation` emits COOP/COEP/CORP headers on all platforms. Availability
of `crossOriginIsolated`, SharedArrayBuffer and WASM threads still depends on the
installed WebView engine; the option does not promise those capabilities.

The mobile HTTP origin supports GET/HEAD files, POST reports and host-message
long polling. It deliberately closes connections after one request; chunked
request uploads, HTTP byte-range responses and conditional caching are not
implemented. This does not replace Go's general-purpose HTTP server. No browser
selection, desktop profiles, executable discovery or extension installation is
introduced by the mobile runner.

## Verification

- `go test ./...` in `res/web`, `scripts/web-ios.sh`, `scripts/web-android.sh`:
  shared policy and portable regression tests.
- `scripts/web-workspace-ios.sh --simulator <UDID>`: real WKWebView tests for
  module imports, workers, CSP rejection, HTML injection order, messaging,
  exit/close/cancellation/timeouts, confinement, tokens and protocol headers.
- `scripts/web-workspace-android.sh --without-androidx` (or with AndroidX): real
  Android WebView tests and loopback protocol checks, also usable against the
  packaged SDK with `--aar <path> --minify`.
