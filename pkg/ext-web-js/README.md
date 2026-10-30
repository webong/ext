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

Import `createExtensionCatalog` and `createTabMessenger` from
`@ext/web-js/native`. The catalog uses an extension-supplied `management` API
to list and describe extensions and watch installed, enabled, disabled, and
uninstalled changes. It is read-only and requires the browser's `management`
permission. Watchers return a cleanup function.

The tab messenger sends a bounded JSON message to an explicit tab ID. Builders
may supply `attach(tabId)` and `isMissingReceiver(error)` to install their own
content receiver and retry once when it is absent. Attachment is limited to
HTTP(S) tabs. The builder decides which tab to target, which content script to
attach, and which methods to expose. These helpers do not select an active tab
or grant permissions.

```js
import { createExtensionCatalog, createTabMessenger } from '@ext/web-js/native';

const catalog = createExtensionCatalog(browser.management);
const stopWatching = catalog.watch(change => console.log(change));
const tabs = createTabMessenger({ tabs: browser.tabs });
const reply = await tabs.send(tabId, { channel: 'my-extension', type: 'ping' });
stopWatching();
```

Run `npm test` for the protocol harness and `npm run build:examples` to create
independent unpacked Chromium examples in `dist/host` and `dist/guest`. Load both
in `chrome://extensions` with Developer mode. Copy each extension ID into the
other extension's popup: admit the guest in the host popup, then connect in the
guest popup. Refresh the host's peer list and try the calls. Reloading either
extension exercises reconnection. The examples use only the `storage` permission.

The package accepts either `browser.runtime` or `chrome.runtime`, or an injected
runtime. Browser smoke testing and Firefox support remain to be completed; the
current examples target Chromium Manifest V3. See the [implementation plan](../../docs/ext-web-js-plan.md).
