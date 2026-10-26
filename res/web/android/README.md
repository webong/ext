# res/web/android

The Android module of [`res/web`](../README.md): the package `io.github.webong.ext.web`, the portable rules
for browser cookies, userscripts and bookmarklets written out in plain Java, with no dependency and no native
code. The classes are in `src/main/java` and their tests in `src/test/java`; `scripts/web-android.sh` compiles
and runs only these, so it needs a JDK alone and nothing of the plugin code can be used.

[`pkg/ext-android`](../../../pkg/ext-android/README.md) is the AAR an app adds: its release script compiles
these sources into the same `classes.jar` as the plugin code (`scripts/ext-android-release.sh`).

## Using it

`io.github.webong.ext.web` is the part of `res/web` a phone needs: the portable rules for browser
cookies, userscripts and bookmarklets, in plain Java with no dependency.

```java
Site site = Site.parse("https://Example.com:8443/a?b=1");      // host lower-cased, escapes as Go's net/url
Cookie cookie = Cookie.builder("sid", token, ".example.com", "/").secure(true).build();
CookieRules.validate(cookie);                                   // field rules; the error never has the value
CookieRules.matches(site, cookie, false);                       // domain, path, Secure and expiry

UserscriptRecord record = Userscript.inferMetadata(new UserscriptRecord("app", "", "", null, null, source), "").signed();
Userscript.validate(record);                                    // document-start, @grant none, explicit matches
String script = Userscript.replaySource(record.id, record.revision, record.source,
        record.matches, record.excludeMatches);
// App code: register `script` to run at document start, for example with androidx.webkit's
// WebViewCompat.addDocumentStartJavaScript(webView, script, allowedOriginRules).

String url = Bookmarklet.encode("alert(document.title)");       // a javascript: URL, wrapped and percent-encoded
String source = Bookmarklet.decode(url);                        // back to source for review; never evaluated
```

- Errors are checked exceptions: `SiteException`, `CookieException`, `UserscriptException` (its `code()` is
  the stable, language-neutral code the fixtures check) and `BookmarkletException`.
- `Cookie`, `CookieBundle` and `UserscriptRecord` read and write the wire form with `fromJson` and
  `toJson`. A cookie's `toString()` prints its name, domain and path and never its value.
- `UserscriptRecord.matches` and `excludeMatches` may be null because the revision digest tells `null`
  from an empty list.
- The rules are Go's, written out for Java. They are checked two ways: the language-neutral fixtures in
  `res/web/testdata` (`cookie/v2`, `userscript/v1alpha1`, `bookmarklet/v1alpha1`), which the Go tests run too,
  and `scripts/web-differential.sh`, which compares the module with Go on about 94,000 generated URLs,
  cookie domains, names and bookmarklet strings (all agree; Go 1.27 with `GODEBUG=urlstrictcolons=1`).
- Desktop SQLite cookie readers, native extension signing/installation and browser profile tooling remain host/adapter responsibilities.
  Mobile JSON member names match exactly where Go ignores case.
  Userscript persistence is covered in [workspace management](../WORKSPACE_MANAGEMENT.md).

```sh
scripts/web-android.sh        # javac -Xlint:all -Werror and the tests; needs a JDK only
```


## Native browser workspace

The `src/android` source set owns the Android WebView workspace and uses the
portable rules in `src/main`. `pkg/ext-android` compiles both into its AAR.
The JDK-only rules tests still build only `src/main` and `src/test`; native tests
live in `src/androidTest`. See [workspace usage](WORKSPACE.md).

See [local web bundle runners](../BUNDLE_RUNNER.md) for native HTML/module execution, CSP, console events, host messaging and lifecycle handling.

See [mobile cookie queries](../COOKIE_QUERY.md) for host-authorized source ordering, merge/first modes, fallback, provenance and cancellation.

See [mobile extension artifacts](../EXTENSION_ARTIFACTS.md) for inspection, unsigned ZIP packaging, revision-checked staging and artifact helpers.
