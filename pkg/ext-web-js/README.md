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

Run `npm test` for the protocol harness and `npm run build:examples` to create
independent unpacked Chromium examples in `dist/host` and `dist/guest`. Load both
in `chrome://extensions` with Developer mode. Copy each extension ID into the
other extension's popup: admit the guest in the host popup, then connect in the
guest popup. Refresh the host's peer list and try the calls. Reloading either
extension exercises reconnection. The examples use only the `storage` permission.

The package accepts either `browser.runtime` or `chrome.runtime`, or an injected
runtime. Browser smoke testing and Firefox support remain to be completed; the
current examples target Chromium Manifest V3. See the [implementation plan](../../docs/ext-web-js-plan.md).
