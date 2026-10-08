# The web engine

`res/web/bundle` runs web content, meaning an HTML page or a JavaScript entry with
its WebAssembly and assets, in a browser the **host** chooses, and reports what the
page did. It is an ext library, not an adapter and not part of any one product: any
program can use it. It depends only on the Go standard library.

## Using it

```go
result, err := bundle.Run(ctx, bundle.Options{
    Root:    "./dist",                   // the bundle; served read-only
    Entry:   "index.html",               // or a .js/.mjs entry
    Timeout: time.Minute,
    Console: func(e bundle.Event) { fmt.Println(e.Level, e.Text) },
    Open: func(ctx context.Context, url string) error {
        // Show url in whatever the host selected: a browser profile, a simulator,
        // an emulator or a device. Return once it has been requested.
        return exec.CommandContext(ctx, "open", "-a", "Safari", url).Run()
    },
})
```

`Run` starts a server on `127.0.0.1`, calls `Open`, and waits. `Result.Status` says
how it ended:

| Status | Meaning |
| --- | --- |
| `exited` | the page called `window.ext.exit(code)`; `ExitCode` is the code |
| `closed` | the tab or window went away, or the page stopped reporting |
| `timeout` | `Options.Timeout` elapsed |
| `failed` | the page could not be opened, or never loaded |

`Run` returns an error only when the run could not start; how a started run ended
is always in the `Result`.

## What the page sees

The server puts a small script first in the page's `<head>`. It defines
`window.ext.log(...)` and `window.ext.exit(code)`, forwards `console.*` and
uncaught errors to `Options.Console`, and reports that the page is alive. A
JavaScript entry gets a generated page around it. Nothing in the bundle has to know
about ext.

## Cross-origin isolation

`Options.CrossOriginIsolation` serves every response with
`Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Embedder-Policy:
require-corp` and `Cross-Origin-Resource-Policy: same-origin`. Browsers allow
`SharedArrayBuffer`, and so WebAssembly threads or a worker blocking on a shared
queue, only on pages that are cross-origin isolated. It is off by default because it
also refuses cross-origin resources that do not opt in, which suits a bundle served
from one origin but not every page. [`examples/plugin-webview`](../examples/plugin-webview)
uses it to host a WASI plugin guest in a browser.

## The page API and the host channel

The page script is one file, `res/web/bundle/shim.js`, used the same way on every
transport. The Swift and Java hosts embed a generated copy of it, so after **any** change
to `shim.js` run `scripts/plugin-mobile-assets.sh` and commit the two files it writes, or
the Plugin SDK CI job fails (`--check` verifies them). A page sees:

| | |
| --- | --- |
| `window.ext.log(...)` | report a line to the host |
| `window.ext.exit(code)` | end the run with a code |
| `window.ext.host.send(string)` | send a message to the host |
| `window.ext.host.onmessage = fn` | receive messages from the host; the page assigns it |
| `window.ext.host.transport` | `"http"`, `"wkwebview"`, `"android"` or `"none"` |

Messages are strings, so `ext.plugin/v1` frames pass through unchanged. `console.*`
and uncaught errors are reported to the host as well, with no code in the page.

**Over loopback** (`Run`), the page posts to the engine's server and polls it for
host messages. A program using `Run` sets `Options.OnMessage` to receive what the
page sends, and `Options.OnReady` to get the function that sends to the page.

**In an embedded host** (an app with a webview), there is no server. The host
injects `shim.js` unchanged at document start (`bundle.Shim()` returns it, with its
token placeholder in place) and the page reports through the app instead:

| | Page to host | Host to page |
| --- | --- | --- |
| iOS | `window.webkit.messageHandlers.ext.postMessage(json)`; register a `WKScriptMessageHandler` named `ext` | `evaluateJavaScript("window.ext.host._deliver(<string literal>)")` |
| Android | `window.ExtHost.postMessage(json)`; `addJavascriptInterface` with the name `ExtHost` | `evaluateJavascript("window.ext.host._deliver(<string literal>)")` |

The page-to-host argument is a JSON object with a `kind`:

| `kind` | other fields | meaning |
| --- | --- | --- |
| `host` | `data` (the string) | the page called `window.ext.host.send` |
| `log` | `level`, `text` | console output or `window.ext.log`; level is `log`, `info`, `warn`, `error`, `debug` or `exception` |
| `exit` | `code` | the page called `window.ext.exit` |
| `alive` | | HTTP transport only: sent at load and every second |
| `closed` | | HTTP transport only: the page is going away |

A native host owns its webview and knows when it closes, so the native transports do
not send `alive` or `closed` at all. The loopback engine uses them to notice a closed
tab, so only the HTTP transport sends them.

## What it protects, and what it does not

- **No network by default.** Every response carries a content security policy that
  limits connections, scripts, images, fonts and media to the bundle's own origin.
  `Policy.AllowNet` opens everything, and `Policy.AllowOrigins` allows named origins.
  Inline scripts and WebAssembly compilation stay allowed, because bundles need them.
- **Only the bundle is served.** A path with `..`, a symbolic link out of the
  bundle, a dotfile, or a directory is refused, not collapsed to something else.
- **Only this run's page can report.** The reporting endpoints carry a random
  token, accept only requests from the page's own origin, and answer only the exact
  loopback address, so another site, or a rebound DNS name, cannot end or feed the
  run.
- **It is not a complete sandbox.** A content security policy cannot stop a page
  from navigating the whole window to another address, and the page runs in
  whichever browser profile the host opens. Run a bundle you do not trust in a
  dedicated profile or simulator. The engine's isolation is **restricted**:
  network-limited, not capability-sandboxed like the `wasm` and `evm` adapters.

## Showing the page

`Open` is the host's decision, which is why the engine needs no native code:

- **A desktop browser:** `open -a "Google Chrome" URL` on macOS, or the host's
  browser adapter.
- **The iOS Simulator:** `xcrun simctl openurl booted URL`. The simulator shares the
  host's network, so loopback works.
- **An Android emulator or device:** `adb reverse tcp:PORT tcp:PORT`, then
  `adb shell am start -a android.intent.action.VIEW -d URL`, so the device's
  `localhost` reaches the host.

## Verified

The same bundle (a WebAssembly module, DOM updates, console output, an uncaught
error, a blocked outside fetch, and an exit code) ran with identical results in
Safari, Chrome and Firefox on macOS, in Mobile Safari in the iOS Simulator, and in
Chrome on an Android 14 emulator through `adb reverse`. Closing a real Chrome tab
ended the run as `closed`.

What running it on real browsers taught:

- **A fresh browser may be waiting for the user.** Chrome on a new Android emulator
  showed its first-run and notification screens, and the page loaded behind them
  only once they were dismissed. Until then the run fails with "the page did not
  load" and says why that can happen. A host that drives a device should clear those
  screens first.
- **The browser's own extensions run on the page.** A userscript extension in the
  desktop Chrome profile logged a line to the page's console, and the engine
  reported it like any other. Console output is not only the bundle's. Use a
  dedicated profile for content you do not control, and expect noise otherwise.
- **Background tabs.** Browsers slow timers in hidden tabs, so the heartbeat backstop
  is generous (15 seconds); a closed tab is normally noticed at once through the
  page's own report.

Not exercised: Windows and Linux browsers, and real iOS and Android devices as
opposed to a simulator and an emulator.

The two-way channel (the page answering a host's `ping` with `pong`, then exiting on
`bye`) ran in Chrome, Safari and Firefox on macOS, in Mobile Safari in the iOS
Simulator, and in Chrome on an Android emulator. The iOS and Android native
transports above were checked only by running the page script against mock message
handlers; no app has used them yet.

## A second, independent run (reported, not re-run here)

Another agent re-ran the checks on 2026-10 with no physical device attached
(`adb devices -l` empty, `xcrun devicectl list devices` found none), so these are
emulator and simulator results only:

| Where | Test | Result |
| --- | --- | --- |
| Android 14 emulator, Chrome 113 | WASI plugin guest | pass: instantiate 487 ms, handshake 628 ms, echo 17 ms |
| Android 14 emulator | two-way channel | pass: ready 306 ms, echo 27 ms |
| Android 14 emulator | outside fetch | blocked by the content security policy |
| Android 14 emulator | Back button | `closed`, reason "the page stopped reporting" (the heartbeat, not an immediate report) |
| iOS 18.3 Simulator, Safari 18.3 | WASI plugin guest | pass: instantiate 118 ms, handshake 112 ms, echo 2 ms |
| iOS 18.3 Simulator | two-way channel | pass: ready 1,480 ms, echo 12.5 ms |
| iOS 18.3 Simulator | outside fetch | blocked |
| In-app WKWebView (`pkg/plugin-ios`, Rust reactor) | handshake and echo | pass: start-up 8.24 s, handshake 38 ms, Unicode echo 1.9 ms |

Two things worth knowing from it: the first two attempts to open the page in the
simulator's Safari failed with "Operation timed out" (error 60) and the third worked, so
a host that drives the simulator should retry the open; and closing by the Back button
was noticed only by the heartbeat timeout, so a closed page can take up to the heartbeat
period to be reported. Still unverified: physical phones, iOS close detection, the
Android in-app WebView host, and the full native conformance suite.

