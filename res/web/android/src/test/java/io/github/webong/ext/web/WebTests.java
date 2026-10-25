package io.github.webong.ext.web;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/**
 * Runs the language-neutral vectors of {@code res/web/testdata}, which the Go tests also run, and
 * the behavior of the port. No test framework is assumed: it exits non-zero on any failure.
 * {@code EXT_WEB_TESTDATA} names another copy of the vectors; {@code EXT_WEB_DIFF} names a corpus
 * the Go implementation wrote (see {@code res/web/internal/portcorpus}).
 */
public final class WebTests {
    private WebTests() {}

    private static int checks;
    private static int failures;
    private static String current = "";

    static void check(boolean ok, String message) {
        checks++;
        if (!ok) {
            failures++;
            System.err.println("FAIL " + current + ": " + message);
        }
    }

    static void eq(Object want, Object got, String message) {
        check(Objects.equals(want, got), message + ": want " + want + ", got " + got);
    }

    private interface Body {
        void run() throws Exception;
    }

    private static void test(String name, Body body) {
        current = name;
        int before = failures;
        try {
            body.run();
        } catch (Throwable t) {
            failures++;
            t.printStackTrace();
            System.err.println("FAIL " + name + ": threw " + t);
        }
        System.out.println((failures == before ? "ok   " : "FAIL ") + name);
    }

    // ---------------------------------------------------------------------------------------

    private static Path testdata() {
        String path = System.getenv("EXT_WEB_TESTDATA");
        return Paths.get(path != null ? path : "res/web/testdata");
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> load(String file) throws IOException {
        String text = new String(Files.readAllBytes(testdata().resolve(file)), StandardCharsets.UTF_8);
        return (Map<String, Object>) Json.parse(text);
    }

    @SuppressWarnings("unchecked")
    private static List<Map<String, Object>> cases(Map<String, Object> file, String key) {
        List<Map<String, Object>> out = new ArrayList<>();
        for (Object item : (List<Object>) file.get(key)) {
            out.add((Map<String, Object>) item);
        }
        check(!out.isEmpty(), key + " has cases");
        return out;
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> map(Object value) {
        return (Map<String, Object>) value;
    }

    private static String str(Map<String, Object> m, String key) {
        return Json.string(m, key);
    }

    // -------------------------------------------------------------------------------- cookies

    private static void cookieFixtures() throws Exception {
        for (Map<String, Object> c : cases(load("cookie/v2/site.json"), "cases")) {
            String name = str(c, "name");
            boolean valid = Boolean.TRUE.equals(c.get("valid"));
            try {
                Site site = Site.parse(str(c, "input"));
                check(valid, name + ": accepted");
                eq(str(c, "expect"), site.string(), name);
            } catch (SiteException e) {
                check(!valid, name + ": refused");
            }
        }
        for (Map<String, Object> c : cases(load("cookie/v2/domain-match.json"), "cases")) {
            eq(c.get("match"), CookieRules.domainMatches(str(c, "siteHost"), str(c, "cookieDomain")), str(c, "name"));
        }
        for (Map<String, Object> c : cases(load("cookie/v2/path-match.json"), "cases")) {
            eq(c.get("match"), CookieRules.pathMatches(str(c, "requestPath"), str(c, "cookiePath")), str(c, "name"));
        }
        for (Map<String, Object> c : cases(load("cookie/v2/cookie.json"), "cases")) {
            Cookie cookie = Cookie.fromObject(map(c.get("cookie")));
            boolean valid;
            try {
                CookieRules.validate(cookie);
                valid = true;
            } catch (CookieException e) {
                valid = false;
            }
            eq(c.get("valid"), valid, str(c, "name"));
            // An error must not carry the secret value.
            Cookie secret = cookie.toBuilder().build();
            Cookie withSecret = Cookie.builder(secret.name, "s3cr3t-value-0f7a", secret.domain, secret.path)
                    .id(secret.id).ref(secret.ref).expiry(secret.expiry).secure(secret.secure).httpOnly(secret.httpOnly)
                    .sameSitePolicy(secret.sameSitePolicy).partitionKey(secret.partitionKey)
                    .hasCrossSiteAncestor(secret.hasCrossSiteAncestor).build();
            try {
                CookieRules.validate(withSecret);
            } catch (CookieException e) {
                check(!e.getMessage().contains("s3cr3t"), str(c, "name") + ": the error leaks the value");
            }
        }
        for (Map<String, Object> c : cases(load("cookie/v2/bundle.json"), "cases")) {
            boolean valid;
            try {
                CookieRules.validate(CookieBundle.fromObject(map(c.get("bundle"))));
                valid = true;
            } catch (CookieException e) {
                valid = false;
            }
            eq(c.get("valid"), valid, str(c, "name"));
        }
    }

    private static boolean cookieValid(Cookie cookie) {
        try {
            CookieRules.validate(cookie);
            return true;
        } catch (CookieException e) {
            return false;
        }
    }

    private static void cookieBehavior() throws Exception {
        String secret = "s3cr3t-value-0f7a";
        Cookie cookie = Cookie.builder("sid", secret, "example.com", "/").partitionKey("https://top.example").build();
        CookieBundle bundle = new CookieBundle("adapter", "https://example.com/", cookie);
        for (String text : new String[] {cookie.toString(), bundle.toString(), String.valueOf(Collections.singletonList(cookie)),
                String.valueOf(Collections.singletonMap("k", cookie))}) {
            check(!text.contains(secret), "a description printed the value: " + text);
        }
        check(cookie.toString().contains("sid") && cookie.toString().contains("<redacted>"), "the description names the cookie");

        Cookie full = Cookie.builder("sid", "v", ".example.com", "/").id(7).ref("r").expiry(5).secure(true).httpOnly(true)
                .sameSitePolicy("lax").partitionKey("p").hasCrossSiteAncestor(true).attribute("x.y", "z").build();
        eq("{\"id\":7,\"ref\":\"r\",\"name\":\"sid\",\"value\":\"v\",\"domain\":\".example.com\",\"path\":\"/\",\"expiry\":5,"
                + "\"secure\":true,\"http_only\":true,\"same_site_policy\":\"lax\",\"partition_key\":\"p\","
                + "\"has_cross_site_ancestor\":true,\"attributes\":{\"x.y\":\"z\"}}", full.toJson(), "full cookie JSON");
        eq(full, Cookie.fromJson(full.toJson()), "cookie JSON round trip");
        eq("{\"name\":\"sid\",\"value\":\"v\",\"domain\":\"example.com\",\"path\":\"/\",\"expiry\":0,\"secure\":false,\"http_only\":false}",
                Cookie.builder("sid", "v", "example.com", "/").build().toJson(), "minimal cookie JSON");
        eq(Cookie.builder("a", "", "", "").build(), Cookie.fromJson("{\"name\":\"a\",\"attributes\":null}"), "missing members are zero");
        eq(bundle, CookieBundle.fromJson(bundle.toJson()), "bundle JSON round trip");

        Cookie session = Cookie.builder("a", "", "d", "/").build();
        check(CookieRules.isActive(session, 1000), "a session cookie is active");
        check(CookieRules.isActive(session.toBuilder().expiry(1001).build(), 1000), "unexpired");
        check(!CookieRules.isActive(session.toBuilder().expiry(1000).build(), 1000), "expired at the instant");

        Site site = Site.parse("https://a.example.com/x/y?q=1");
        Cookie secure = Cookie.builder("a", "v", ".example.com", "/x").secure(true).build();
        check(CookieRules.matches(site, secure, false), "matches");
        check(!CookieRules.matches(Site.parse("http://a.example.com/x"), secure, false), "a secure cookie is not sent over http");
        check(CookieRules.matches(null, secure, false), "no site matches on expiry alone");
        check(!CookieRules.matches(site, Cookie.builder("a", "v", ".example.com", "/z").build(), false), "other path");
        check(CookieRules.sameListed(secure, secure), "same listed");
        check(!CookieRules.sameListed(secure, secure.toBuilder().attribute("a.b", "c").build()), "attributes differ");

        for (String domain : new String[] {"[::1]", "::1", "fe80::1", "::ffff:1.2.3.4"}) {
            Cookie c = Cookie.builder("a", "v", domain, "/").build();
            // A bracketed text is not an address, and neither is anything with a zone.
            eq(!domain.equals("[::1]"), cookieValid(c), domain);
            check(!cookieValid(Cookie.builder("a", "v", domain + "%eth0", "/").build()), domain + "%eth0");
        }
    }

    private static void siteBehavior() throws Exception {
        Site site = Site.parse("HTTPS://Example.COM:8443/a%20b/c?x=1&y=%41#frag%20ment");
        eq("https", site.scheme, "scheme");
        eq("example.com:8443", site.host, "host");
        eq("example.com", site.hostname, "hostname");
        eq("/a b/c", site.path, "path");
        eq("/a%20b/c", site.escapedPath, "escaped path");
        eq("x=1&y=%41", site.rawQuery, "query");
        eq("frag ment", site.fragment, "fragment");
        eq("https://example.com:8443/a%20b/c?x=1&y=%41#frag%20ment", site.string(), "string");

        eq("fe80::1%en0", Site.parse("http://[FE80::1%25en0]:80/").hostname, "zone host");
        for (String raw : new String[] {"http://[1.2.3.4]/", "http://[::1/", "http://a[::1]/", "https://a.example:b/", "https://a:1:2/",
                "https://exa mple.com/", "https://a.com/%zz", "https://a.com/\u0007", "https:example.com", "https:/example.com",
                "//example.com/", "javascript:alert(1)", "https://@example.com/", "https://a.com#%zz", "HTTP", "https://%41.com/"}) {
            try {
                Site.parse(raw);
                check(false, "accepted " + raw);
            } catch (SiteException expected) {
                check(true, raw);
            }
        }
        Site bucher = Site.parse("https://BÜCHER.example/");
        eq("bücher.example", bucher.host, "non-ASCII host");
        eq("https://b%C3%BCcher.example/", bucher.string(), "non-ASCII canonical form");
        eq("bücher.example", Site.parse("https://b%C3%BCcher.example/").host, "escaped non-ASCII host");
    }

    // ------------------------------------------------------------------------------ userscripts

    private static UserscriptRecord fixtureRecord(Map<String, Object> o) {
        return new UserscriptRecord(str(o, "target"), str(o, "id"), str(o, "name"), str(o, "revision"), Json.strings(o, "matches"),
                Json.strings(o, "excludeMatches"), false, str(o, "source"));
    }

    private static UserscriptRecord base() {
        return new UserscriptRecord("session", "demo", "demo", Arrays.asList("https://example.com/*"),
                Collections.<String>emptyList(), "console.log(1);\n");
    }

    private static String code(UserscriptRecord record) {
        try {
            Userscript.validate(record);
            return "";
        } catch (UserscriptException e) {
            return e.code();
        }
    }

    private static void userscriptFixtures() throws Exception {
        String dir = "userscript/v1alpha1/";
        for (Map<String, Object> c : cases(load(dir + "match-patterns.json"), "cases")) {
            UserscriptRecord r = base();
            r = new UserscriptRecord(r.target, r.id, r.name, "", Arrays.asList(str(c, "pattern")), r.excludeMatches, false, r.source);
            String error = str(c, "error");
            eq(error, code(r.signed()), str(c, "name"));
            eq(Boolean.valueOf(error.isEmpty()), c.get("valid"), str(c, "name") + ": fixture inconsistent");
        }
        for (Map<String, Object> c : cases(load(dir + "directives.json"), "cases")) {
            UserscriptRecord r = new UserscriptRecord("session", "demo", "demo", "", Json.strings(c, "matches"),
                    Json.strings(c, "excludeMatches"), false, str(c, "source"));
            eq(str(c, "error"), code(r.signed()), str(c, "name"));
        }
        for (Map<String, Object> c : cases(load(dir + "metadata.json"), "cases")) {
            UserscriptRecord got = Userscript.inferMetadata(new UserscriptRecord("", "", "", null, null, str(c, "source")),
                    str(c, "sourceFile"));
            Map<String, Object> want = map(c.get("expect"));
            eq(str(want, "name"), got.name, str(c, "name"));
            eq(str(want, "id"), got.id, str(c, "name"));
            eq(Json.strings(want, "matches"), got.matches, str(c, "name"));
            eq(Json.strings(want, "excludeMatches"), got.excludeMatches, str(c, "name"));
        }
        List<Map<String, Object>> revisions = cases(load(dir + "revision.json"), "cases");
        check(revisions.size() >= 4, "enough revision vectors");
        Map<String, String> seen = new HashMap<>();
        for (Map<String, Object> c : revisions) {
            String want = str(c, "revision");
            eq(want, Userscript.revision(fixtureRecord(map(c.get("record")))), str(c, "name"));
            check(!seen.containsKey(want), "vectors " + seen.get(want) + " and " + str(c, "name") + " share a digest");
            seen.put(want, str(c, "name"));
        }
        for (Map<String, Object> c : cases(load(dir + "record.json"), "cases")) {
            UserscriptRecord r = fixtureRecord(map(c.get("record")));
            if (r.revision.equals("auto")) {
                r = r.signed();
            }
            eq(str(c, "error"), code(r), str(c, "name"));
        }
        for (Map<String, Object> c : cases(load(dir + "replay.json"), "cases")) {
            String error = str(c, "error");
            try {
                String script = Userscript.replaySource(str(c, "id"), str(c, "revision"), str(c, "source"), Json.strings(c, "matches"),
                        Json.strings(c, "excludeMatches"));
                eq("", error, str(c, "name") + ": produced a script");
                eq(str(c, "expect"), script, str(c, "name"));
            } catch (UserscriptException e) {
                eq(error.equals("-") ? "" : error, e.code(), str(c, "name"));
            }
        }
    }

    private static void userscriptBehavior() throws Exception {
        UserscriptRecord nullExcludes = new UserscriptRecord("t", "i", "n", Arrays.asList("https://a/*"), null, "s");
        UserscriptRecord emptyExcludes = new UserscriptRecord("t", "i", "n", Arrays.asList("https://a/*"),
                Collections.<String>emptyList(), "s");
        check(nullExcludes.toJson().contains("\"excludeMatches\":null"), nullExcludes.toJson());
        check(emptyExcludes.toJson().contains("\"excludeMatches\":[]"), emptyExcludes.toJson());
        eq(nullExcludes, UserscriptRecord.fromJson(nullExcludes.toJson()), "null round trip");
        eq(emptyExcludes, UserscriptRecord.fromJson(emptyExcludes.toJson()), "empty round trip");
        check(!Userscript.revision(nullExcludes).equals(Userscript.revision(emptyExcludes)), "null and empty differ");

        String source = "// ==UserScript==\n// @name Hello World\n// @match https://a.example/*\n// ==/UserScript==\nconsole.log(1);\n";
        UserscriptRecord inferred = Userscript.inferMetadata(new UserscriptRecord("t", "", "", null, null, source), "");
        eq("hello-world", inferred.id, "inferred id");
        eq("", code(inferred.signed()), "an inferred record validates");
        UserscriptRecord other = new UserscriptRecord(inferred.target, inferred.id, inferred.name, "", Arrays.asList("https://other.example/*"),
                inferred.excludeMatches, false, inferred.source);
        eq("match_metadata_mismatch", code(other.signed()), "header and record disagree");

        // Go's ToLower maps U+0130 to "i" and the Kelvin sign to "k".
        eq("ik-x-y", Userscript.inferMetadata(new UserscriptRecord("t", "", "İK-X  y.", null, null, ""), "").id, "id folding");
        // "\r\n" ends a line at the "\n".
        eq("unsupported_grant", code(new UserscriptRecord("session", "demo", "demo", Arrays.asList("https://example.com/*"),
                Collections.<String>emptyList(), "// @grant GM_x\r\nconsole.log(1);\r\n").signed()), "CRLF lines");
    }

    // ----------------------------------------------------------------------------- bookmarklets

    private static boolean bookmarkletRejects(String source) {
        try {
            Bookmarklet.encode(source);
            return false;
        } catch (BookmarkletException e) {
            return true;
        }
    }

    private static void bookmarkletFixtures() throws Exception {
        String dir = "bookmarklet/v1alpha1/";
        Map<String, Object> encode = load(dir + "encode.json");
        for (Map<String, Object> c : cases(encode, "cases")) {
            eq(str(c, "url"), Bookmarklet.encode(str(c, "source")), str(c, "name"));
        }
        for (Map<String, Object> c : cases(encode, "rejects")) {
            check(bookmarkletRejects(str(c, "source")), "encode accepted " + str(c, "name"));
        }
        Map<String, Object> decode = load(dir + "decode.json");
        for (Map<String, Object> c : cases(decode, "cases")) {
            eq(str(c, "source"), Bookmarklet.decode(str(c, "url")), str(c, "name"));
        }
        for (Map<String, Object> c : cases(decode, "rejects")) {
            try {
                Bookmarklet.decode(str(c, "url"));
                check(false, "decode accepted " + str(c, "name"));
            } catch (BookmarkletException expected) {
                check(true, str(c, "name"));
            }
        }
        Map<String, Object> page = load(dir + "install-page.json");
        for (Map<String, Object> c : cases(page, "cases")) {
            String html = Bookmarklet.installPage(str(c, "pageName"), str(c, "source"));
            for (Object want : (List<?>) c.get("contains")) {
                check(html.contains((String) want), str(c, "name") + ": lacks " + want);
            }
            for (Object bad : (List<?>) c.get("absent")) {
                check(!html.contains((String) bad), str(c, "name") + ": contains " + bad);
            }
        }
        for (Map<String, Object> c : cases(page, "rejects")) {
            try {
                Bookmarklet.installPage(str(c, "pageName"), str(c, "source"));
                check(false, "installPage accepted " + str(c, "name"));
            } catch (BookmarkletException expected) {
                check(true, str(c, "name"));
            }
        }
    }

    private static void bookmarkletBehavior() throws Exception {
        for (String source : new String[] {"alert(1)", "a + b % 2;\n// 100%\n", "x = '%41%zz';", "日本語 😀", "  padded  "}) {
            eq(source, Bookmarklet.decode(Bookmarklet.encode(source)), "round trip");
        }
        // A "%41" needs two characters after the percent sign, so a lone "%4" stays literal.
        eq("a%4", Bookmarklet.decode("javascript:a%4"), "lone percent");
        eq("aA", Bookmarklet.decode("javascript:a%41"), "escape");
        eq("}).call(window);", Bookmarklet.decode("javascript:void%20%28function%28%29%7B%0A%7D%29.call%28window%29%3B"),
                "the wrapper parts can share their newline");
        check(bookmarkletRejects("JAVASCRİPT:alert(1)"), "U+0130 lower-cases to i in Go");
        check(bookmarkletRejects("a\u0000b"), "NUL");
        check(bookmarkletRejects("a\uD800b"), "an unpaired surrogate is not UTF-8");
        try {
            Bookmarklet.decode("data:text/html,x");
            check(false, "decode accepted data:");
        } catch (BookmarkletException expected) {
            check(true, "data:");
        }
        eq("alert(1)", Bookmarklet.decode("JavaScript:alert(1)"), "the scheme is case-insensitive");
    }

    // ------------------------------------------------------------------------------ differential

    @SuppressWarnings("unchecked")
    private static void differential() throws Exception {
        String path = System.getenv("EXT_WEB_DIFF");
        if (path == null) {
            System.out.println("skip differential (EXT_WEB_DIFF is not set)");
            return;
        }
        Map<String, Object> corpus = map(Json.parse(new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8)));
        List<String> bad = new ArrayList<>();
        List<Object> sites = (List<Object>) corpus.get("sites");
        for (Object item : sites) {
            Map<String, Object> c = map(item);
            String input = str(c, "input");
            Site site = null;
            try {
                site = Site.parse(input);
            } catch (SiteException e) {
                // refused
            }
            boolean ok = Boolean.TRUE.equals(c.get("ok"));
            if ((site != null) != ok) {
                bad.add("site " + input + ": ok go=" + ok + " java=" + (site != null));
                continue;
            }
            if (site == null) {
                continue;
            }
            List<String> got = Arrays.asList(site.string(), site.hostname, site.host, site.path, site.escapedPath, site.rawQuery,
                    site.forceQuery ? "T" : "F", site.fragment);
            List<String> want = Arrays.asList(str(c, "string"), str(c, "hostname"), str(c, "host"), str(c, "path"), str(c, "escapedPath"),
                    str(c, "rawQuery"), Boolean.TRUE.equals(c.get("forceQuery")) ? "T" : "F", str(c, "fragment"));
            if (!got.equals(want)) {
                bad.add("site " + input + ": java=" + got + " go=" + want);
            }
        }
        report("sites", bad, sites.size());

        bad = new ArrayList<>();
        List<Object> domains = (List<Object>) corpus.get("domains");
        for (Object item : domains) {
            Map<String, Object> c = map(item);
            boolean valid = cookieValid(Cookie.builder("a", "v", str(c, "domain"), "/").build());
            if (valid != Boolean.TRUE.equals(c.get("valid"))) {
                bad.add("domain " + str(c, "domain") + ": go=" + c.get("valid") + " java=" + valid);
            }
        }
        report("domains", bad, domains.size());

        bad = new ArrayList<>();
        List<Object> ids = (List<Object>) corpus.get("ids");
        for (Object item : ids) {
            Map<String, Object> c = map(item);
            String name = str(c, "name");
            UserscriptRecord record;
            if (name.startsWith("file:")) {
                record = Userscript.inferMetadata(new UserscriptRecord("", "", "", null, null, ""), name.substring(5));
            } else if (name.startsWith("direct:")) {
                record = Userscript.inferMetadata(new UserscriptRecord("", "", name.substring(7), null, null, ""), "");
            } else {
                record = Userscript.inferMetadata(
                        new UserscriptRecord("", "", "", null, null, "// ==UserScript==\n// @name " + name + "\n// ==/UserScript==\n"), "");
            }
            if (!record.id.equals(str(c, "id")) || !record.name.equals(str(c, "outName"))) {
                bad.add("id " + name + ": go=(" + str(c, "id") + "," + str(c, "outName") + ") java=(" + record.id + "," + record.name + ")");
            }
        }
        report("ids", bad, ids.size());

        bad = new ArrayList<>();
        List<Object> marks = (List<Object>) corpus.get("bookmarklets");
        for (Object item : marks) {
            Map<String, Object> c = map(item);
            String input = str(c, "input");
            String url = null;
            try {
                url = Bookmarklet.encode(input);
            } catch (BookmarkletException e) {
                // refused
            }
            if ((url != null) != Boolean.TRUE.equals(c.get("encodeOK")) || (url != null && !url.equals(str(c, "url")))) {
                bad.add("encode " + input + ": go=" + c.get("url") + " java=" + url);
            }
            String source = null;
            try {
                source = Bookmarklet.decode(input);
            } catch (BookmarkletException e) {
                // refused
            }
            if ((source != null) != Boolean.TRUE.equals(c.get("decodeOK")) || (source != null && !source.equals(str(c, "source")))) {
                bad.add("decode " + input + ": go=" + c.get("source") + " java=" + source);
            }
        }
        report("bookmarklets", bad, marks.size());
    }

    private static void report(String label, List<String> bad, int total) {
        System.out.println(label + ": " + (total - bad.size()) + "/" + total + " agree");
        for (int i = 0; i < Math.min(25, bad.size()); i++) {
            System.out.println("  MISMATCH " + bad.get(i));
        }
        check(bad.isEmpty(), label + ": " + bad.size() + " of " + total + " differ");
    }

    private static void workspaceScripts() throws Exception {
        UserscriptRecord record = new UserscriptRecord("page", "script", "Script", "",
                List.of("https://example.com/*"), null, true, "window.test = true;").signed();
        eq(1, WorkspaceScripts.prepare(List.of(record), "page").size(), "enabled registration");
        try { WorkspaceScripts.prepare(List.of(record), "other"); check(false, "target accepted"); }
        catch (IllegalArgumentException expected) { check(true, "target rejected"); }
        try { WorkspaceScripts.prepare(List.of(record, record), "page"); check(false, "duplicate accepted"); }
        catch (IllegalArgumentException expected) { check(true, "duplicate rejected"); }
        try { WorkspaceScripts.prepare(List.of(record.withRevision("tampered")), "page"); check(false, "revision accepted"); }
        catch (UserscriptException expected) { check(true, "revision rejected"); }
        UserscriptRecord disabled = new UserscriptRecord(record.target, record.id, record.name, record.revision,
                record.matches, record.excludeMatches, false, record.source);
        eq(0, WorkspaceScripts.prepare(List.of(disabled), "page").size(), "disabled registration");
    }

    public static void main(String[] args) {
        test("workspace scripts", WebTests::workspaceScripts);
        test("cookie fixtures", WebTests::cookieFixtures);
        test("cookie workflows", CookieWorkflowTests::run);
        test("management protocol", ManagementTests::run);
        test("userscript store", UserscriptStoreTests::run);
        test("cookie behavior", WebTests::cookieBehavior);
        test("site behavior", WebTests::siteBehavior);
        test("userscript fixtures", WebTests::userscriptFixtures);
        test("userscript behavior", WebTests::userscriptBehavior);
        test("bookmarklet fixtures", WebTests::bookmarkletFixtures);
        test("bookmarklet behavior", WebTests::bookmarkletBehavior);
        test("differential", WebTests::differential);
        System.out.println(checks + " checks, " + failures + " failures");
        if (failures != 0) {
            System.exit(1);
        }
    }
}
