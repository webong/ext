# Browser peer compatibility

This records **loaded extension** checks, separately from `npm test` and the
example build. Run the same host/guest flow after changing the peer protocol or
example manifests. A passing row covers only the flows listed here.

| Browser | Build checked | Admission, state/event, calls both ways, denial | Background restart and fresh registration | Result |
| --- | --- | --- | --- | --- |
| Chrome for Testing (macOS arm64) | 156.0.8078.80 | Automated smoke | Automated smoke, host and guest workers | Pass on 2026-10-30 |
| Firefox (macOS arm64) | 159.0 | Automated smoke | Automated smoke, host and guest add-on reloads | Pass on 2026-10-30 |
| Safari | — | — | — | Unchecked |

## Chromium: automated

Install Chromium or Chrome for Testing and point `CHROMIUM_BINARY` to its
executable. From this package:

```sh
CHROMIUM_BINARY='/path/to/Google Chrome for Testing' npm run smoke:chromium
```

The command builds both independent extensions, starts an isolated temporary
profile, loads them unpacked, discovers their generated IDs, and runs the table's
flows. It stops each MV3 service worker through the DevTools protocol and checks
that a new worker and peer registration appear. It removes the profile afterward.
It does not use the user's browser profile or install extensions there.

Google Chrome 137 and newer branded builds ignore `--load-extension`; use
Chromium or Chrome for Testing for this automated loader. A branded Chrome
check requires loading `dist/host` and `dist/guest` through `chrome://extensions`.

## Firefox: automated

Point `FIREFOX_BINARY` to Firefox's executable. From this package:

```sh
FIREFOX_BINARY='/Applications/Firefox.app/Contents/MacOS/firefox' npm run smoke:firefox
```

The command builds both Firefox examples, launches an isolated headless profile,
temporarily installs the add-ons through WebDriver BiDi, runs the table's flows,
reloads each add-on, then closes Firefox and removes the profile. It uses no
existing Firefox profile. The caller needs a Firefox build with WebDriver BiDi
extension installation support.

## Firefox: manual fallback

Run `npm run build:examples`. Open `about:debugging#/runtime/this-firefox` and
load `dist/firefox-host/manifest.json` and `dist/firefox-guest/manifest.json` as
temporary add-ons. Their fixed IDs are `ext-peer-host@example.org` and
`ext-peer-guest@example.org`.

1. Open the host popup, enter the guest ID, and select **Admit guest**.
2. Open the guest popup, enter the host ID, and select **Connect**. Select
   **Status** and expect `{"connected":true}`.
3. Open the host popup again. Its result JSON should include `guestState` with
   `status: "ready"` and `lastEvent.kind: "guest.ready"`. Select **Refresh peers**;
   expect the guest ID.
4. In the guest popup, select **Call host**; expect `"Hello, guest"`. Select
   **Call denied method**; expect `{"error":"denied"}`.
5. In the host popup, select **Call guest echo**; expect `{"value":"Hello"}`.
6. Reload the host add-on in `about:debugging`. Wait for the guest popup's
   **Status** to return `connected:true`, then repeat **Refresh peers** and
   **Call host**. Reload the guest add-on, then repeat **Status**, **Refresh
   peers**, and **Call guest echo**.
7. Remove both temporary add-ons after the check. Record the Firefox version,
   date, and outcome in the table above.

The browser controls add-on loading and removal. This check covers peer
messaging; it does not verify management, themes, userscripts, or store
installation APIs.
