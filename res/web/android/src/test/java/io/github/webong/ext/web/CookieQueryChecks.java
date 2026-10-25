package io.github.webong.ext.web;

import java.util.*;
import java.nio.charset.StandardCharsets;

/** Shared JVM/device behavior checks; no JDK-only filesystem APIs. */
public final class CookieQueryChecks {
    private CookieQueryChecks() {}
    public interface Check { void accept(boolean ok, String name); }
    public static void run(Check check) throws Exception {
        Cookie good = Cookie.builder("sid", "secret", "example.test", "/").build();
        Cookie wrong = Cookie.builder("other", "leak", "example.test", "/").build();
        CookieQuery.Options o = new CookieQuery.Options(); o.url = "https://example.test";
        CookieQuery.Result r = CookieQuery.get(o, (ctx, options) -> new CookieQuery.Selection(Arrays.asList(
            new CookieQuery.Source("test", "work", "bad", (c, q) -> { throw new Exception("credential-secret"); }),
            new CookieQuery.Source("test", "work", "legacy", new CookieQuery.Legacy() {
                public List<Cookie> list(CookieQuery.Context c, Site s) { return Collections.singletonList(good); }
                public Cookie export(CookieQuery.Context c, Site s, Cookie row) { return wrong; }
            }),
            new CookieQuery.Source("test", "work", "good", (c, q) -> new CookieQuery.Batch(Collections.singletonList(good), Collections.singletonList("partial"), "/private/store")))
            , Collections.singletonList("discovery")));
        check.accept(r.cookies.size() == 1 && r.cookies.get(0).source.equals("good"), "query partial success and legacy identity binding");
        check.accept(r.warnings.size() == 4 && !r.warnings.toString().contains("credential-secret"), "query warning redaction");
        check.accept(!r.toString().contains("secret") && !r.cookies.get(0).toString().contains("secret"), "query result redaction");
        check.accept(r.cookies.get(0).sourceInfo.storePath.equals("/private/store"), "query store provenance");
        o.inline = ("[" + good.toJson() + "]").getBytes(StandardCharsets.UTF_8); o.timeoutMillis = 100;
        try {
            CookieQuery.get(o, (c,q) -> { Thread.sleep(150); return new CookieQuery.Selection(Collections.emptyList()); });
            check.accept(false, "query timeout");
        } catch (CookieQuery.Failure e) { check.accept(e.kind == CookieQuery.Failure.Kind.TIMEOUT && e.partial.cookies.size() == 1, "query timeout retains partial results"); }
        o.timeoutMillis = 0;
        CookieQuery.Cancellation cancellation = new CookieQuery.Cancellation();
        try {
            CookieQuery.get(o, (c,q) -> { cancellation.cancel(); c.check(); return null; }, cancellation);
            check.accept(false, "query cancellation");
        } catch (CookieQuery.Failure e) { check.accept(e.kind == CookieQuery.Failure.Kind.CANCELLED && e.partial.cookies.size() == 1, "query cancellation retains partial results"); }
        o.mode = "first"; o.fallbackInline = "invalid".getBytes(StandardCharsets.UTF_8);
        try { CookieQuery.get(o, null); check.accept(false, "query validates unused fallback"); }
        catch (CookieException expected) { check.accept(true, "query validates unused fallback"); }
        o.fallbackInline = null; o.mode = "merge"; o.inline = null;
        o.preferredSource = "test:work"; o.profiles.put("test", "work");
        r = CookieQuery.get(o, (c,q) -> {
            check.accept(q.preferredSource.equals("test:work") && q.profiles.get("test").equals("work"), "query passes host selection");
            q.url = "https://unrelated.test"; // The backend cannot change the orchestration snapshot.
            return new CookieQuery.Selection(Collections.singletonList(new CookieQuery.Source("test", "work", "legacy", new CookieQuery.Legacy() {
                public List<Cookie> list(CookieQuery.Context context, Site site) { return Collections.singletonList(good); }
                public Cookie export(CookieQuery.Context context, Site site, Cookie listed) { return listed; }
            })));
        });
        check.accept(r.cookies.size() == 1, "query legacy export success");
        o.url = ""; o.allowAllHosts = true;
        r = CookieQuery.get(o, (c,q) -> new CookieQuery.Selection(Collections.singletonList(new CookieQuery.Source("test", "work", "legacy", new CookieQuery.Legacy() {
            public List<Cookie> list(CookieQuery.Context context, Site site) { throw new AssertionError("unsupported legacy call"); }
            public Cookie export(CookieQuery.Context context, Site site, Cookie listed) { throw new AssertionError("unsupported legacy export"); }
        }))));
        check.accept(r.cookies.isEmpty() && r.warnings.size() == 1, "query refuses unsupported legacy all-host access");
        o.sources.add("test:work"); o.browsers.add("test");
        try { CookieQuery.get(o, null); check.accept(false, "query ambiguous selection"); }
        catch (CookieQuery.Failure e) { check.accept(e.kind == CookieQuery.Failure.Kind.INVALID_OPTIONS, "query rejects ambiguous selection"); }
        o = new CookieQuery.Options(); o.url = "https://example.test";
        r = CookieQuery.get(o, (c,q) -> new CookieQuery.Selection(Collections.singletonList(new CookieQuery.Source("test", "work", "rows", (ctx,request) ->
            new CookieQuery.Batch(Arrays.asList(Cookie.builder("bad name", "secret", "example.test", "/").build(), good))))));
        check.accept(r.cookies.size() == 1 && r.warnings.size() == 1, "query skips invalid source rows");
        o.inline = ("[" + good.toJson() + "]").getBytes(StandardCharsets.UTF_8);
        try {
            CookieQuery.get(o, (c,q) -> new CookieQuery.Selection(Collections.singletonList(new CookieQuery.Source("", "work", "invalid", (ctx,request) -> new CookieQuery.Batch(Collections.emptyList())))));
            check.accept(false, "query invalid descriptor");
        } catch (CookieQuery.Failure e) { check.accept(e.kind == CookieQuery.Failure.Kind.INVALID_SOURCE && e.partial.cookies.size() == 1, "query invalid descriptor retains partial results"); }
    }
}
