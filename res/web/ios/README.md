# res/web/ios

The iOS module of [`res/web`](../README.md): the SwiftPM package `ExtWeb`, the portable rules for
browser cookies, userscripts and bookmarklets written out in Swift, without the plugin engine.
It is a package of its own (`Package.swift` here), so it builds and tests with Swift alone; nothing of the
plugin code can be imported into it.

[`pkg/ext-ios`](../../../pkg/ext-ios/README.md) is the package an app adds: it ships these sources beside
its plugin code (its `Sources/ExtWeb` is a link to `Sources/ExtWeb` here, and a release stages a real copy).
Edit the code here, not through the link.

## Using it

`ExtWeb` is the part of `res/web` a phone needs: the portable rules for browser cookies, userscripts
and bookmarklets, without the plugin engine.

```swift
import ExtWeb

let site = try parseSite("https://Example.com:8443/a?b=1")        // host lower-cased, escapes as Go's net/url
let cookie = Cookie(name: "sid", value: token, domain: ".example.com", path: "/", secure: true)
try CookieRules.validate(cookie)                                   // field rules; the error never has the value
CookieRules.matches(site, cookie)                                  // domain, path, Secure and expiry

var record = Userscript.inferMetadata(UserscriptRecord(target: "app", id: "", name: "", source: source))
record.revision = Userscript.revision(of: record)                   // sha256 over the reviewed fields
try Userscript.validate(record)                                     // document-start, @grant none, explicit matches
let script = try Userscript.replaySource(id: record.id, revision: record.revision, source: record.source,
                                         matches: record.matches, excludes: record.excludeMatches)
// App code: webView.configuration.userContentController.addUserScript(
//     WKUserScript(source: script, injectionTime: .atDocumentStart, forMainFrameOnly: false))

let url = try Bookmarklet.encode("alert(document.title)")           // a javascript: URL, wrapped and percent-encoded
let source = try Bookmarklet.decode(url)                            // back to source for review; never evaluated
```

- `Cookie`, `CookieBundle` and `UserscriptRecord` are `Codable` with the wire names of `res/web`; a cookie's
  `description`, `debugDescription` and `dump` print its name, domain and path and never its value.
- `UserscriptRecord.matches` and `excludeMatches` are optional because the revision digest tells `null`
  from `[]`.
- The rules are Go's, written out for Swift. They are checked two ways: the language-neutral fixtures in
  `res/web/testdata` (`cookie/v2`, `userscript/v1alpha1`, `bookmarklet/v1alpha1`), which the Go tests run too,
  and `scripts/web-differential.sh`, which compares the module with Go on about 94,000 generated URLs,
  cookie domains, names and bookmarklet strings (all agree; Go 1.27 with `GODEBUG=urlstrictcolons=1`).
- Desktop SQLite cookie readers, native extension signing/installation and browser profile tooling remain host/adapter responsibilities.
  Mobile JSON member names match exactly where Go ignores case, and a host with several
  colons is refused (Go's default follows the consuming module's `go` line).
  Userscript persistence is covered in [workspace management](../WORKSPACE_MANAGEMENT.md).

```sh
scripts/web-ios.sh            # swift test of this package; needs Swift, not the engine xcframework
```


## Native browser workspace

The separate `ExtWebKit` target in this package owns the WKWebView workspace and
uses the portable `ExtWeb` userscript rules. Both products are shipped by
`pkg/ext-ios`; native source lives here. See [workspace usage](WORKSPACE.md).
`scripts/web-ios.sh` selects the portable rules tests;
`scripts/web-workspace-ios.sh` selects the native WebKit integration tests.

See [local web bundle runners](../BUNDLE_RUNNER.md) for native HTML/module execution, CSP, console events, host messaging and lifecycle handling.

See [mobile cookie queries](../COOKIE_QUERY.md) for host-authorized source ordering, merge/first modes, fallback, provenance and cancellation.

See [mobile extension artifacts](../EXTENSION_ARTIFACTS.md) for inspection, unsigned ZIP packaging, revision-checked staging and artifact helpers.
