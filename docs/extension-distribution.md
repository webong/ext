# Extension distribution through CTX

CTX provides build artifacts and browser adapter installation routes. A service
can build a catalog, private store, or web install interface on top of those
routes. The service owns its catalog, hosting, publisher accounts, approval UI,
and download delivery. Browser-specific formats and installation rules stay in
the selected ext adapter.

## Publishing a Chrome extension

A publishing service can use this workflow:

1. Put the intended update XML URL in the extension's `manifest.json` as
   `update_url` before preparing the source.
2. Prepare and review the source, then sign it with a caller-provided Chrome
   executable. Keep the publisher's private key for subsequent versions.
3. Use the resulting CRX ID, version, and artifact revision to generate update
   XML. CTX derives ID and version from the verified CRX rather than accepting
   unrelated catalog metadata.
4. Host the CRX, XML, and the service's web install interface. Publish only the
   intended public artifacts; never expose the signing key or private build
   directory.
5. Let the user choose a supported installation route for their browser and OS.

The build machine can run macOS, Windows, or Linux. Chrome's Linux-only rules
for local CRX/server installation apply to the **user's installation machine**.
Building a CRX on a Mac does not make local CRX installation available in that
Mac's Chrome profile.

```sh
# Obtain a reviewed source revision from the result.
ctx browser manage extension prepare --target chrome:Default \
  --input '{"source":"/srv/build/private/extension"}'

# The initial build generates a private .pem beside this private output.
# Subsequent releases supply keyPath pointing to that retained private key.
ctx browser manage extension sign --target chrome:Default \
  --input '{"executable":"/path/to/chrome","source":"/srv/build/private/extension","revision":"sha256:SOURCE_REVISION","output":"/srv/build/private/extension.crx"}'

# Use artifactRevision from sign, or revision from prepare on the CRX.
ctx browser manage extension update_manifest --target chrome:Default \
  --input '{"source":"/srv/build/private/extension.crx","revision":"sha256:ARTIFACT_REVISION","codebaseURL":"https://store.example/releases/extension-1.0.crx","output":"/srv/public/updates.xml"}'
```

`extension.sign` returns `id`, `version`, `artifactRevision`, and the private
`keyPath` to its caller. CRX3 inspection verifies RSA/SHA-256 or P-256 ECDSA
proofs, signed extension identity, and the bounded ZIP payload. It is also
available as `extension.prepare` on a `.crx` file, or through the owning
`adapters/chromium/engine.InspectCRX` Go API. Inspection is not publisher trust,
a Chrome Web Store review, or proof that a particular browser will accept the
extension.

`extension.update_manifest` returns `prepared` with ID, version, revision,
codebase URL, and either an exclusive `.xml` output path or an `xml` string
when output is omitted. The Go API is
`chromium.CRXUpdateManifest(source, revision, codebaseURL, output)`.
Neither route uploads anything or contacts the server. Generate one XML file
per extension; a catalog can serve many such entries. CRX3 artifacts are limited
to 64 MiB and their headers to 1 MiB. The manifest version string must follow
Chrome's numeric version rules.

Serve CRX files with `Content-Type: application/x-chrome-extension` and update
XML with `Content-Type: application/xml`. Use HTTPS for hosted releases. The
XML codebase must refer to the hosted CRX, whose signed identity and version
match the generated entry. Increase the extension version and sign with the
same private key for updates. Keep its `update_url` aligned with the XML's
public URL. See Chrome's [Linux hosting and update requirements](https://developer.chrome.com/docs/extensions/how-to/distribute/host-on-linux).

## Web install interface

For Chrome users on Linux, the service can provide a direct CRX download/install
link using Chrome's documented content type, plus the self-hosted update URL.
For example, a Linux-specific button can link to the hosted release:

```html
<a href="https://store.example/releases/extension-1.0.crx">Install for Chrome on Linux</a>
```

Chrome processes this browser installation flow; the server's CTX process is a
builder, not a process controlling the user's computer. Browser behavior and
user consent still determine whether installation succeeds. To register an
external installation request, the user or an authorized local application
must run CTX on the installation machine. A website alone cannot write the
user's Chrome external preferences or Windows registry. CTX does not currently
provide a browser-to-local-CTX URL protocol or a localhost installation daemon.

For Chrome users on macOS/Windows, offer a Chrome Web Store link or a source ZIP
for manual Developer mode / Load unpacked. CTX can stage a reviewed ZIP into a
persistent local directory and guide that existing route. A direct self-hosted
CRX is not an ordinary external installation route on those systems. Other
browsers retain their own requirements: Firefox uses a signed XPI, while Safari
uses a signed containing app and user enablement in Settings. Query the selected
adapter's capabilities before offering an install option.

These platform rules come from Chrome's [alternative installation methods](https://developer.chrome.com/docs/extensions/how-to/distribute/install-extensions#prereq-crx).

## Preferences files and Windows registry

`extension.store_install` and `extension.store_remove` select the native
registration mechanism automatically. The same named-store input works across
Chrome's supported platforms:

```sh
ctx browser manage extension store_install --target chrome:Default \
  --input '{"id":"EXTENSION_ID","store":"chrome"}'

ctx browser manage extension store_remove --target chrome:Default \
  --input '{"id":"EXTENSION_ID","store":"chrome"}'
```

For macOS and Linux, the adapter creates an `<id>.json` preferences file:

```json
{"external_update_url":"https://clients2.google.com/service/update2/crx"}
```

On macOS, the default user location is
`~/Library/Application Support/Google/Chrome/External Extensions`. An
administrator can select the all-users location explicitly:

```sh
ctx browser manage extension store_install --target chrome:Default \
  --input '{"id":"EXTENSION_ID","store":"chrome","externalDirectory":"/Library/Application Support/Google/Chrome/External Extensions"}'
```

System-wide macOS paths must be owned by root with group `admin` or `wheel`,
must not be world-writable, and must contain no symbolic links. Directories must
also be traversable by all users of that system-wide registration. The adapter
checks existing path components before writing and checks the completed file.
It reports a permissions error rather than changing existing ownership or
permissions. User-specific registration does not require this all-users
ownership rule. New preference files use mode 0644 and newly created directories
use 0755 even under a restrictive installer umask; existing directories retain
their permissions. Request removal rejects symlink/non-regular preference files
and files that do not match the selected request.

On Windows, the adapter writes a `REG_SZ` named `update_url` beneath the
machine extension ID key:

| Windows OS | Chrome registration key |
|---|---|
| 32-bit | `HKEY_LOCAL_MACHINE\Software\Google\Chrome\Extensions\<id>` |
| 64-bit | `HKEY_LOCAL_MACHINE\Software\Wow6432Node\Google\Chrome\Extensions\<id>` |

The Windows helper explicitly opens the 32-bit registry view, so the correct
location is selected independently of the CTX or PowerShell executable's
architecture. Administrator rights are required. `externalDirectory` is rejected
on Windows because that route uses the registry. Removal verifies the exact
URL and string value type and refuses keys with additional values or subkeys.
The helper reports the actual OS-specific registry location in the result.

macOS/Windows named-store registration returns
`awaiting-browser-confirmation`. Restart Chrome and enable the extension through
its prompt or extensions page. Neither a preference write nor a registry write
proves the browser has installed or enabled it. Both routes retain Chrome's
Web Store requirement; they cannot register an arbitrary local CRX or private
update server on those platforms. Linux supports the local/server preferences
described below. See Chrome's [preferences-file instructions](https://developer.chrome.com/docs/extensions/how-to/distribute/install-extensions#preferences)
and [Windows registry instructions](https://developer.chrome.com/docs/extensions/how-to/distribute/install-extensions#registry).

## Register a self-hosted update server on Linux

The Chrome adapter accepts an explicit `updateURL` instead of a named store:

```sh
ctx browser manage extension store_install --target chrome:Default \
  --input '{"id":"EXTENSION_ID","updateURL":"https://store.example/updates.xml"}'
```

The adapter creates `<id>.json` containing `external_update_url` in its
documented Chrome external extensions directory. This registers a request and
returns `requested`; it does not report `installed`. Chrome fetches the XML and
CRX when processing the request. CTX does not fetch or pre-approve remote code.
The ID must be 32 characters from `a` through `p`, and the URL must be absolute
HTTP(S) without embedded credentials or a fragment.

The default Linux location is `/opt/google/chrome/extensions`; the alternate
`/usr/share/google-chrome/extensions` can be selected through
`externalDirectory`. Both are machine-level locations and normally require
administrator rights. Selecting `chrome:Default` dispatches through that
adapter; it does not make the request specific to one Chrome profile.

Use `store_remove` with the same ID and update URL to remove the exact request.
Removal refuses a file containing different or additional metadata. Browser
uninstallation choices remain respected: registration does not clear Chrome's
external-extension blocklist.

## Register a local CRX on Linux

A local catalog can supply a reviewed CRX artifact directly:

```sh
ctx browser manage extension prepare --target chrome:Default \
  --input '{"source":"/opt/my-store/extension.crx"}'

ctx browser manage extension store_install --target chrome:Default \
  --input '{"source":"/opt/my-store/extension.crx","revision":"sha256:ARTIFACT_REVISION"}'
```

The adapter derives ID and version from the verified CRX and creates
`external_crx` and `external_version` preferences. Optional supplied `id` or
`version` must match the artifact. Keep the CRX path available and readable by
Chrome. This request remains pending until Chrome processes it; it is not a
profile-specific verified installation. CTX does not replace an existing
registration silently. Remove the old matching request before registering a
new local artifact/version.

To remove a request, pass its original ID, source path, and version. Removal
works even if the CRX has already been deleted:

```sh
ctx browser manage extension store_remove --target chrome:Default \
  --input '{"id":"EXTENSION_ID","source":"/opt/my-store/extension.crx","version":"1.0"}'
```

Custom server/local CRX registration is enabled explicitly by Chrome's adapter
configuration on Linux. Other Chromium-derived adapters do not inherit this
permission merely because they share the engine. macOS/Windows reject these
inputs before writing any registration. Existing named Web Store registration
remains available on its declared platforms.

## Capability discovery

`extension.capabilities` exposes:

- `externalStoreRequest` and `supportedStores` for named-store registration.
- `externalLocalPackage` for local signed-package registration.
- `externalUpdateURL` for a caller-provided update server.
- `externalInstallScope`: `machine` or the default `browser-user` scope; an
  explicitly selected system directory can widen a user-default route.
- `updateManifest` for native publishing metadata generation.
- Existing `persistentLocalInstall`, `requiresBrowserAction`, and `sessionLoad`
  fields for local/manual installation and temporary activation.

The store request chooses exactly one source: a named `store`, an `updateURL`,
or a local CRX `source`. A CRX `revision` is required for registration, and
`version` is derived unless supplied for a consistency check. Publishing XML
uses `codebaseURL`, which is separate from the XML server's `updateURL`.
A service can keep its own catalog model and translate a selected entry into
these adapter inputs. CTX does not require a CTX-specific marketplace format.

## Validation status

The implementation builds locally and for Windows/Linux; vet passes. Live browser
acceptance, actual Windows registry execution, macOS system-wide registration,
Linux external registration, hosted update fetching, and publisher UI flows still
need validation in an appropriate browser environment. CRX signature checks do
not replace Chrome's native package, permissions, or policy checks.
