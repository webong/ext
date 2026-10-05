# Browser profile management

CTX exposes reusable profile-scoped workflows for browser extensions,
userscripts, and bookmarklets. A trusted browser adapter owns the
browser-specific operations. The same adapter can keep display, page, and
session operations behind a separate runtime and implement the public CTX
backend interface as a bridge to that runtime.

Browser-specific behavior lives under `adapters/<name>/`. The shared manager
handles the protocol, portable package preparation, userscript storage, and
bookmarklet export. Native discovery, capability reporting, signing, store
registration, installation, and page execution are supplied by the adapter's
backend; the shared manager has no browser-name dispatch or native fallback.
Related browsers can reuse the native engine owned by their family adapter,
with product paths, store URLs, and supported routes supplied by each adapter.

## CLI

Choose a profile explicitly or select it with `ctx set browser <adapter>:<profile>`:

~~~sh
ctx browser manage extension targets --target firefox:Profile\ 1
ctx browser manage extension prepare --target chrome:Default \
  --input '{"source":"/path/to/extension.zip"}'
ctx browser manage extension install --target firefox:Profile\ 1 \
  --input '{"source":"/path/to/signed-extension.xpi","revision":"sha256:…"}'
ctx browser manage extension activate --target chrome:Profile\ 1 \
  --input '{"source":"/path/to/extension.zip","destination":"/tmp/ctx-extension","revision":"sha256:…"}'
ctx browser manage userscript prepare --target firefox:Profile\ 1 \
  --input '{"sourceFile":"/path/to/script.user.js","id":"example","name":"Example","matches":["https://example.com/*"]}'
ctx browser manage bookmarklet encode --target firefox:Profile\ 1 \
  --input '{"sourceFile":"/path/to/bookmarklet.js"}'
~~~

The adapter must declare each operation it implements in
`browser_management = "kind.action,..."`. CTX rejects undeclared operations,
checks adapter trust, and validates protocol version, response identity, and
the separation between extension installation and session activation. Use
`--input-file PATH` or `--input-file -` for structured or larger JSON input.

## Operations

The operation catalog lists `extension` actions `targets`, `capabilities`,
`prepare`, `package`, `sign`, `stage`, `install`, `activate`,
`store_install`, `store_remove`, `update_manifest`, `convert`, and `policy`.
Preparation inspects the package and produces a revision for review. Packaging
and signing produce artifacts. Staging copies files for a later browser action.
Installation uses the selected browser's native or guided install route.
Activation loads an extension into a controlled session and must not claim a
persistent installation. Store requests only register a browser-native request;
they are not proof that an extension is installed or enabled.

`userscript` supports `prepare`, `install`, `update`, `list`, `describe`,
`enable`, `disable`, `uninstall`, and `activate`. `bookmarklet` supports
`encode`, `decode`, and `install_page`. A bookmarklet is exported for the user
to save and click; encoding it does not execute it or edit browser bookmarks.

The request is a JSON object with `version`, `kind`, `action`, and optional
operation-specific `input`. The response has `version`, `kind`, `action`,
`status`, and an optional JSON `result`. Input and response sizes are bounded.
Status values remain adapter-defined, subject to the install/activation
boundary validated by CTX.

## Go adapter API

`github.com/webong/ctx/res/browser/contract` exposes the request and response
types, operation catalog, and validators. The sibling `res/browser/guest`
package provides `ManagementBackend` and `RunManagement`.
Implement `ManageBrowser` to connect the protocol to browser-specific logic:

~~~go
type managementBackend struct{}

func (managementBackend) ManageBrowser(
    ctx context.Context,
    profile string,
    request contract.ManagementRequest,
) (contract.ManagementResponse, error) {
    // Dispatch an operation to the browser provider and its page/session runtime.
    result := contract.ManagementResult(map[string]string{"profile": profile})
    return contract.ManagementResponse{
        Version: contract.ManagementVersion,
        Kind: request.Kind,
        Action: request.Action,
        Status: "prepared",
        Result: result,
    }, nil
}

func main() {
    invocation, err := adapter.Parse(os.Args[1:])
    if err != nil { os.Exit(2) }
    if invocation.Operation == "manage" {
        os.Exit(guest.RunManagement(context.Background(), invocation.Selection, os.Stdin, os.Stdout, os.Stderr, managementBackend{}))
    }
}
~~~

The contract package exposes `PageSessionRuntime` and `PageSession` for explicit
target discovery, page navigation, script injection, and userscript replay.
The provider can call its page/session runtime through this interface while
keeping CDP, WebDriver BiDi, and replay behavior behind that implementation.
The current adapter runner dispatches the versioned request from stdin. The
provider must preserve native browser consent and policy requirements, verify
package revisions before install or activation, and report persistence only
when its native route verifies it. Callers can add review, approval, and audit
around this reusable API without making their higher-level policy part of CTX.

All 18 first-party browser adapters declare the shared local operations:
`extension.targets`, `extension.capabilities`, `extension.prepare`,
`extension.package`, `extension.sign`, and `extension.stage`; all userscript
storage and metadata operations; and all bookmarklet encoding/export operations.
Chrome, Chromium, and Edge add consent-based persistent installation,
temporary CDP activation, and session userscript activation. Firefox adds
signed-XPI installation and temporary WebDriver BiDi activation. Safari opens a signed app and reports that the user
must enable it in Safari Settings. Chrome and Edge add external store
request/removal and platform-specific managed policies. Safari adds Xcode project
conversion. Chrome, Chromium, Edge, and Firefox expose native page sessions. Each adapter manifest lists its own supported operations.
Platform limits and install requirements appear in `extension.capabilities`.

`extension.targets` discovers known local browser executables and profiles; it
does not read history or cookies. Install and activation requests can select a
fresh `targetId`, or use the profile chosen with `--target`. Chromium install
and activation require an absolute staged `destination` and reviewed `revision`.
The persistent install route opens the browser's extension page and waits for
the user to approve Load unpacked before verifying the extension after restart.
Close the selected profile before installation or activation. Activation starts
a temporary browser session and keeps the command running until that session
closes. Progress is written to stderr while the operation is active. Firefox
installation requires a signed XPI; Firefox activation installs the XPI
temporarily. Safari installation
requires a signed `.app` and always leaves extension enablement to the user.
`userscript.activate` can attach stored scripts to an explicit native page
session, or load one enabled script into a temporary Chromium-family session
when `sessionTarget` is omitted. Scripts run in the page's main JavaScript
world at document start; the host page can inspect or interfere with them.

Store request input uses an extension `id`, optional `store` (`chrome` or
`edge`), and optional `externalDirectory`. It registers a browser-native
request; it does not claim that the browser fetched or enabled the extension.
Named-store requests use preferences JSON on macOS/Linux and the machine's
32-bit registry view on Windows. macOS can select its documented all-users
`externalDirectory`; system ownership/permission checks remain adapter-owned.
Extension signing requires the caller's browser executable, web-ext
credentials, or Xcode project and signing identity, depending on the selected
browser.

Chromium-family adapters also inspect signed CRX3 artifacts through `prepare`
and generate publishing XML through `update_manifest`. Chrome's Linux adapter
accepts custom `updateURL` or a reviewed local CRX `source` in `store_install`;
macOS/Windows retain named-store registration and manual Load unpacked rules.
See [extension distribution](extension-distribution.md) for build, hosting,
web install interfaces, request removal, and capability discovery.

## Shared workflow libraries

Adapters can use CTX's reusable local logic directly:

- `github.com/webong/ctx/res/browser/extension` inspects extension directories,
  ZIPs, and XPIs; creates deterministic ZIPs; stages validated files; and
  exposes portable artifact, capability, and target types and helpers. It
  contains no native browser driver or product discovery tables. Browser
  signing, installation, and activation live in the owning adapter packages.
- `github.com/webong/ctx/res/browser/userscript` validates metadata and source,
  computes review revisions, and stores enabled state in the caller's CTX
  configuration directory. Activation remains session-scoped.
- `github.com/webong/ctx/res/browser/bookmarklet` encodes and decodes bookmarklet
  URLs and creates a reviewable install page. It never executes source or
  edits browser bookmarks.

## Custom executable and profile targets

For a custom profile, select its absolute path with `--target`. Supply an
absolute `executable` if discovery finds no executable or multiple executables.
For Chromium-family adapters the selected path is the **user data root**;
`profileDirectory` selects a child such as `Default`. For Firefox the selected
path is the **profile directory** itself. Optional `profilePath` must match the
selected absolute path. Discovered `targetId` selection is separate from these
explicit path fields; it remains bound to the selected adapter and profile.

~~~sh
ctx browser manage extension install --target 'chrome:/path/to/custom-user-data' \
  --input '{"executable":"/path/to/chrome","profileDirectory":"Default","source":"/path/to/extension","destination":"/path/to/staged-extension","revision":"sha256:…"}'
ctx browser manage extension install --target 'firefox:/path/to/custom-profile' \
  --input '{"executable":"/path/to/firefox","source":"/path/to/signed.xpi","revision":"sha256:…"}'
~~~

Chrome's default user data root cannot use the native debugging verification
route. `extension.install` stages the reviewed files and returns
`awaiting-browser-action` with manual Load unpacked instructions. It never
reports a verified installation for this fallback. Chromium-family native extension
installation and temporary activation use Chromium's debugging pipe on macOS, Linux, and
Windows. Windows passes the two inherited pipe handles through
`--remote-debugging-io-pipes`; the parent retains only its command/response ends.
The selected browser must implement the native Extensions CDP commands.

## Safari project conversion

On macOS, `extension.convert` invokes Xcode's Safari WebExtension packaging or
conversion tool. The adapter stages the reviewed source and checks its revision
again after conversion. The output location must be new and outside the source.

~~~sh
ctx browser manage extension convert --target safari:default \
  --input '{"source":"/path/to/webextension","output":"/path/to/new-project","revision":"sha256:…","bundleId":"com.example.extension","appName":"Example"}'
~~~

Conversion returns `packaged`. Build and sign the generated project through
`extension.sign`, then prepare and install the resulting signed containing app.
Conversion does not install or enable the Safari extension.

## Managed extension policies

`extension.policy` accepts `id`, optional `store`, and `policyAction`:
`force`, `unforce`, `block`, or `unblock`. Chrome supports Windows registry
policies and Linux managed JSON. Edge supports Windows registry policies.
The route requires administrator rights. macOS policy deployment remains an
administrator-managed configuration-profile operation outside this route.

~~~sh
ctx browser manage extension policy --target chrome:Default \
  --input '{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","policyAction":"force"}'
~~~

Policy updates return `policy-updated`, never `installed`. Linux updates use
Chrome's adapter-declared `ctx-extensions.json` file, retain unrelated values,
and reject competing policy keys in other managed files. Windows updates retain
unrelated numbered registry entries and remove only matching extension IDs.
Consult `extension.capabilities` for the current platform's supported drivers.

## Native page sessions

The `session` operations are `targets`, `connect`, `navigate`, `inject`, and
`replay`. Chrome, Chromium, and Edge use CDP; Firefox uses WebDriver BiDi. A
browser must already expose its debugging endpoint. CTX attaches to an explicit
page; these routes never launch or close the caller's browser.

For Chromium, `session.targets` can discover a marker from the selected user
data root or use an explicit `endpoint`. HTTP CDP endpoints resolve through
`json/version`; WebSocket endpoints must identify the browser. Firefox requires
a provider-supplied WebSocket `endpoint`. A sessionless `/session` endpoint creates
an attachment-owned automation session and refuses an already active session.
An explicit `/session/SESSION_ID` URL attaches to the provider's existing session.
Target results include page IDs, endpoint,
protocol, and URL. A debugging marker or endpoint does not prove isolation
between profiles in the same browser process; select the returned page ID.

~~~sh
ctx browser manage session targets --target 'chrome:/path/to/custom-user-data'
ctx browser manage session targets --target firefox:personal \
  --input '{"endpoint":"ws://127.0.0.1:9222/session"}'
ctx browser manage session navigate --target 'chrome:/path/to/custom-user-data' \
  --input '{"target":{"id":"PAGE_ID","protocol":"cdp","endpoint":"http://127.0.0.1:9222"},"url":"https://example.com"}'
ctx browser manage session inject --target 'chrome:/path/to/custom-user-data' \
  --input '{"target":{"id":"PAGE_ID","protocol":"cdp","endpoint":"http://127.0.0.1:9222"},"source":"document.documentElement.dataset.ctx = \"attached\"","options":{"runAt":"now","world":"page"}}'
~~~

`navigate` and immediate `inject` finish and detach. `connect`, `replay`, and
`inject` with `runAt: "document-start"` keep the command attached until
interrupted. Injection supports `now` and `document-start` in the page world.
`replay` accepts `registrations` containing `id`, `revision`, `source`, `matches`,
and optional `excludeMatches`. It registers document-start preloads and restores
scripts in the current document. Each document applies a given ID/revision once.
Disabling or removing a registration prevents future execution; arbitrary page
effects from already executed source cannot be reversed by unregistering it.
Closing the attachment removes its recorded preloads. CDP detaches its target
session. Firefox ends automation sessions CTX created; it removes only CTX's
preloads and event subscription from a provider-owned session, then closes its
socket. Both preserve the browser.

### Automatic transport recovery

Native attachments retry transport loss for up to 30 seconds, with delays from
250 ms to 2 seconds. WebSocket pings detect unresponsive peers after 45 seconds.
Recovery uses the same endpoint and selected page ID, then
restores the last successful userscript registration set and document-start
injections. Userscripts retain their document-local ID/revision guard. Navigation
and immediate injections are not automatically repeated. Closing the selected
page or exhausting the recovery window ends the attachment with an error.

CDP opens a fresh target attachment. Firefox reconnects to the explicit
provider-owned `/session/SESSION_ID` URL, or to a same-origin resumable URL
returned when CTX created the session. Existing BiDi preloads survive socket
loss, so the adapter removes its recorded handles and event subscription before
restoring them. Repeated outages retain partially cleaned ownership state.

Firefox's direct `/session` route does not publish a resumable URL and keeps an
automation session alive when its socket drops. If that session remains active,
CTX reports that recovery is unavailable; it cannot reattach or end that session
through a new sessionless socket. If the provider releases it, CTX can create a
new session and restore the same selected context. For reconnectable Firefox
attachments, supply a session-specific URL from the provider. The native routing
and lifetime behavior are defined in Firefox's
[session registration](https://raw.githubusercontent.com/mozilla-firefox/firefox/main/remote/webdriver-bidi/WebDriverBiDi.sys.mjs)
and [connection implementation](https://raw.githubusercontent.com/mozilla-firefox/firefox/main/remote/webdriver-bidi/WebDriverBiDiConnection.sys.mjs).

~~~sh
ctx browser manage session connect --target firefox:personal \
  --input '{"target":{"id":"CONTEXT_ID","protocol":"bidi","endpoint":"ws://127.0.0.1:9222/session/SESSION_ID"}}'
~~~

Adapter packages also expose `NewPageSessionRuntime(config, endpoint)` factories
implementing the public `PageSessionRuntime` interface. Go consumers can keep
one connection and use `Navigate`, `Inject`, `ReplayUserscripts`, and `Close`.
`PageSessionState` exposes terminal attachment failure, cancellation, or closure;
its `Done` channel stays open during recoverable transport loss. Native commands
have bounded timeouts; callers should provide a fresh cleanup context to `Close`.

## Userscript preparation and live reconciliation

Preparation infers omitted `name`, `id`, `matches`, and `excludeMatches` from
userscript metadata. With no header name it uses the source filename. An
explicit `revision` on install or update must match the prepared source,
identity, target, and scope. Updating preserves the existing enabled state.

To attach enabled stored scripts to a selected page, provide `sessionTarget`
to `userscript.activate`. Optional `id` limits activation to one stored script;
omitting it activates all enabled scripts for the selected adapter/profile.
For the temporary Chromium-family launch route, native `executable`,
`profilePath`, and `profileDirectory` options are passed to the owning adapter;
custom roots use the same absolute `--target` binding as extension operations.

~~~sh
ctx browser manage userscript activate --target firefox:personal \
  --input '{"sessionTarget":{"id":"CONTEXT_ID","protocol":"bidi","endpoint":"ws://127.0.0.1:9222/session"}}'
~~~

The command restores current-document scripts, registers preloads for navigation,
and polls the store every 500 ms to reconcile updates, enable/disable changes,
and removals. Registrations are bounded to 512 scripts and 8 MiB total source.
Malformed records stop activation and trigger native cleanup. Native exceptions
and unrecoverable disconnections are reported as failures. Progress appears on stderr and the
final response is emitted after the session ends.
