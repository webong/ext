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
error, a blocked outside fetch, and an exit code) ran in Safari, Chrome and Firefox
on macOS and in Mobile Safari in the iOS Simulator, with identical results. The
Android recipe is the standard one, but it was **not** run: the emulator would not
start for lack of disk space. Closing a real tab was not exercised either, only
simulated.
