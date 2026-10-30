# ext-web-js

`@ext/web-js` is a standalone package for browser extension peers. An extension
can host methods, consume methods from another extension, or do both. It uses
extension runtime ports and requires no framework or ext plugin engine.

`createHost({ admit, methods })` listens for external peers. `admit(sender)`
receives the browser-supplied sender ID and must explicitly accept it.
`createGuest({ hostId, methods })` connects to a configured host ID. Both return
`start()` and `stop()` methods; constructing a peer performs no browser action.
Methods provide `validateInput`, `validateOutput`, and `handle` functions.
`authorize` can reject individual calls with `PeerError('denied', message)`.

`createSpoke({ name, hostId })` adds extension registration and state replay to
the guest connection. It sends a `register` state message when connected and an
`update` message after `updateState(patch)`. The host receives both through
`onState(sender, value)`, with the browser-supplied sender ID. Builders can pass
`discover()` instead of `hostId`; the spoke periodically checks for a new host
and reconnects when it changes. `discoverExtension(match)` is an optional helper
for applications that have requested the browser's `management` permission.
The application supplies the match rule, admission policy, and event meaning.
For an acknowledged event, expose a host method and call it with `spoke.call()`;
`spoke.event()` is one-way.

`createHub({ admit })` maintains a registry from those registration and update
messages. `hub.spokes()` returns detached snapshots keyed by browser-supplied
extension ID; disconnected spokes leave the registry. Host and spoke methods
remain available for bidirectional calls. A product such as Xallet or Cymonkey
can wrap these APIs with its own identity, permissions, UI, and event routing.

## Browser-native helpers

Import `createExtensionCatalog`, `createExtensionManager`, and
`createTabMessenger` from `@ext/web-js/native`. The catalog uses the in-browser
`management` API
to list and describe extensions and watch installed, enabled, disabled, and
uninstalled changes. It is read-only and requires the browser's `management`
permission. Watchers return a cleanup function.

The manager calls `management.setEnabled`, `uninstall`, and
`getPermissionWarningsById` for an explicit extension ID. Call `setEnabled` or
`uninstall` directly from a user action; the browser may require that gesture
and may show confirmation UI. The library does not bypass browser restrictions
or choose which extensions to manage. Firefox currently restricts
`setEnabled` for regular extensions, so callers must handle a browser rejection.
There is no general in-extension API here to install arbitrary extensions;
Firefox's `management.install` is limited to signed themes.

The tab messenger sends a bounded JSON message to an explicit tab ID. Builders
may supply `attach(tabId)` and `isMissingReceiver(error)` to install their own
content receiver and retry once when it is absent. Attachment is limited to
HTTP(S) tabs. The builder decides which tab to target, which content script to
attach, and which methods to expose. These helpers do not select an active tab
or grant permissions.

```js
import { createExtensionCatalog, createExtensionManager, createTabMessenger } from '@ext/web-js/native';

const catalog = createExtensionCatalog(browser.management);
const manager = createExtensionManager(browser.management);
const stopWatching = catalog.watch(change => console.log(change));
// Inside a button click handler, after your application authorizes this ID:
await manager.setEnabled(extensionId, false);
const tabs = createTabMessenger({ tabs: browser.tabs });
const reply = await tabs.send(tabId, { channel: 'my-extension', type: 'ping' });
stopWatching();
```

## Extension workspace mechanisms

`@ext/web-js/workspace` provides three independent native helpers. Each accepts
browser APIs supplied by the integrating extension:

- `createScopedStorage({storage, namespace})` isolates keys for independent
  features sharing an extension's storage area.
- `createScriptRegistry({scripting, namespace, assetPrefix})` registers and
  removes packaged content scripts under collision-resistant scoped IDs. Files
  must stay under the supplied asset prefix.
- `createRuleRegistry({network, storage, namespace})` records ownership of
  dynamic network-rule IDs and refuses changes by another namespace. It reads
  the current rules before mutation so a failed ownership save can restore the
  previous browser rules.

The caller owns permission requests, manifest declarations, asset review,
rule meaning, and selecting which pages or tabs receive behavior. The helpers
do not run scripts from remote URLs.

## Themes

`@ext/web-js/themes` creates a color-and-image theme object, builds a static
Manifest V3 theme manifest, and applies a live theme where the browser exposes
`browser.theme`. Colors use RGB arrays and images use packaged relative paths.
`npm run build:examples` creates a color-only static theme in `dist/theme`.

```js
import { createThemeManifest, createThemeController } from '@ext/web-js/themes';

const manifest = createThemeManifest({
  name: 'Night', version: '1.0.0', geckoId: 'night@example.org',
  colors: { frame: [24, 32, 54], tab_background_text: [255, 255, 255] },
});
const controller = createThemeController(browser.theme);
await controller.apply(manifest.theme);
```

Live themes require Firefox's `theme` permission and API; unsupported browsers
report `unavailable`. A static theme is a separate add-on artifact that still
needs signing and distribution.

## Userscripts and bridges

`@ext/web-js/userscripts` reconciles browser-native userscript registrations
with a caller-owned catalog. `createUserscriptRegistry` requires `prepare` to
produce a reviewed registration plan and `authorize` before changes. It binds
registration IDs to a namespace and restores missing registrations after a
browser restart. Portable userscript metadata and preparation remain in
`res/web/userscript`.

`@ext/web-js/bridge` provides a nonce-bound `MessagePort` handshake for a
caller-created sandbox page, plus a page-visible request bridge. The sandbox
caller creates the frame, supplies a fresh random nonce, and owns its content
and permission policy. The page bridge exposes only methods in the caller's
allowlist; any script on that page can invoke those methods, so they must be
safe for page use. These APIs carry no Cymonkey or Xallet command vocabulary.

## Host and guest examples

Run `npm run build:examples` in this package. The output contains independently
loadable `dist/host` and `dist/guest` Chromium MV3 extensions, plus
`dist/firefox-host` and `dist/firefox-guest` Firefox MV3 extensions. The Firefox
builds use background modules and the `browser` API. Their fixed add-on IDs are
`ext-peer-host@example.org` and `ext-peer-guest@example.org`.

For Chromium, load `dist/host` and `dist/guest` in `chrome://extensions` with
Developer mode. Copy each extension ID into the other extension's popup: admit
the guest in the host popup, then connect in the guest popup.

In Firefox, open `about:debugging#/runtime/this-firefox` and load each Firefox
directory's `manifest.json` as a temporary add-on. In the host popup, admit
`ext-peer-guest@example.org`; in the guest popup, connect to
`ext-peer-host@example.org`. Check the guest's status and greeting, try the
denied method, then call the guest echo from the host. Temporary add-ons are
removed when Firefox restarts. The examples require no `management` permission.
Reloading either extension exercises reconnection. Run `npm test` for the
protocol harness. The examples use only the `storage` permission.

`res/web/extension` can inspect and package these built directories for an
external installation workflow. Browser installation and activation are supplied
by the selected browser adapter; the embedded JavaScript peer library does not
call those Go APIs.

The package accepts either `browser.runtime` or `chrome.runtime`, or an injected
runtime. The current examples passed automated host/guest checks in Firefox 159
and Chrome for Testing 156, including host and guest background restarts. See
the [browser compatibility checks](COMPATIBILITY.md) and the
[implementation plan](../../docs/ext-web-js-plan.md).

This is a private staging package. Cymonkey will own and distribute the library
after handoff; see [HANDOFF.md](HANDOFF.md).
