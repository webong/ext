package io.github.webong.ext.web.workspacetest;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.android.BrowserWorkspace;
import java.util.Arrays;
import java.util.Collections;

public final class TestActivity extends Activity {
    private BrowserWorkspace workspace;
    private BundleChecks bundles;
    private io.github.webong.ext.web.android.WorkspaceManagement router;
    private io.github.webong.ext.web.UserscriptStore store;
    private int phase, passed, failed;
    private boolean evaluating, done, expectDocumentStart, navigationStarted;

    @Override public void onCreate(Bundle saved) {
        super.onCreate(saved);
        expectDocumentStart = getIntent().getBooleanExtra("documentStart", true);
        try {
            workspace = new BrowserWorkspace(this, "test", new BrowserWorkspace.Listener() {
                @Override public void onStateChange(BrowserWorkspace.State state) {
                    if (navigationStarted && !state.loading && state.progress == 1 && !evaluating && !done) {
                        evaluating = true;
                        workspace.view().evaluateJavascript("window.atStart", TestActivity.this::loaded);
                    }
                }
            });
            setContentView(workspace.view());
            check(workspace.supportsDocumentStartScripts() == expectDocumentStart, "document-start capability");
            try { workspace.navigate("file:///etc/passwd"); check(false, "file navigation rejected"); }
            catch (Exception expected) { check(true, "file navigation rejected"); }
            java.io.File directory = new java.io.File(getFilesDir(), "scripts-" + java.util.UUID.randomUUID());
            store = new io.github.webong.ext.web.UserscriptStore(directory);
            router = new io.github.webong.ext.web.android.WorkspaceManagement(workspace, store, "profile");
            check(route("userscript", "prepare", installInput()).status.equals("prepared"), "route prepare");
            check(store.list("test").isEmpty(), "prepare does not persist");
            check(route("userscript", "install", installInput()).status.equals("installed"), "route install");
            check(new io.github.webong.ext.web.UserscriptStore(directory).load("test", "script").equals(record()), "reopen stored record");
            String targets = route("session", "targets", "{}").result;
            check(targets.contains("webview"), "native target discovery");
            check(route("session", "connect", "{\"target\":" + targets.substring(1, targets.length() - 1) + "}").status.equals("connected"), "native connect with discovered target");
            try { route("session", "navigate", "{\"url\":\"file:///etc/passwd\"}"); check(false, "routed file navigation rejected"); }
            catch (Exception expected) { check(true, "routed file navigation rejected"); }
            try { route("session", "endpoint", "{}"); check(false, "unsupported endpoint rejected"); }
            catch (Exception expected) { check(true, "unsupported endpoint rejected"); }
            try { router.manage("other", new io.github.webong.ext.web.BrowserManagement.Request("userscript", "uninstall", "{\"id\":\"script\"}"), (r,e) -> {}); check(false,"profile mismatch"); }
            catch (Exception expected) { check(true,"profile mismatch"); }
            if (expectDocumentStart) route("userscript", "activate", "{}");
            else {
                try { route("userscript", "activate", "{}"); check(false, "unsupported registration rejected"); }
                catch (UnsupportedOperationException expected) { check(true, "unsupported registration rejected"); }
                workspace.replaceUserscripts(Collections.emptyList());
            }
            testCookies();
            new Handler(Looper.getMainLooper()).postDelayed(() -> {
                if (!done) { check(false, "test timeout"); finishTests(); }
            }, 30000);
        } catch (Exception failure) { fail(failure); }
    }

    private io.github.webong.ext.web.BrowserManagement.Response route(String kind, String action, String input) throws Exception {
        io.github.webong.ext.web.BrowserManagement.Response[] result = {null};
        router.manage("profile", new io.github.webong.ext.web.BrowserManagement.Request(kind,action,input), (response,error) -> {
            if (error != null) throw new IllegalStateException(error);
            result[0] = response;
        });
        if (result[0] == null) throw new IllegalStateException("expected synchronous routing completion");
        return result[0];
    }
    private String installInput() {
        return "{\"id\":\"script\",\"name\":\"Script\",\"matches\":[\"https://example.com/*\"],\"excludeMatches\":[\"https://example.com/private/*\"],\"source\":"
            + io.github.webong.ext.web.WorkspaceInput.quote(record().source) + "}";
    }
    private void injectThen(Runnable next) throws Exception {
        router.manage("profile", new io.github.webong.ext.web.BrowserManagement.Request("session","inject","{\"source\":\"window.routed = 42;\"}"), (response,error) -> {
            if (done) return;
            if (error != null) { fail(error); return; }
            check("injected".equals(response.status),"routed injection completion");
            workspace.view().evaluateJavascript("window.routed", value -> {
                if (done) return;
                check("42".equals(value),"routed injection ran");
                try {
                    router.manage("profile", new io.github.webong.ext.web.BrowserManagement.Request("session","inject","{\"source\":\"throw new Error('fixture');\"}"), (r,e) -> {
                        if (done) return;
                        check(e != null && r == null,"routed script failure"); next.run();
                    });
                } catch (Exception failure) { fail(failure); }
            });
        });
    }

    private void testCookies() throws Exception {
        io.github.webong.ext.web.Cookie cookie = io.github.webong.ext.web.Cookie.builder(
            "ext_cookie_test", "fixture-token", "example.com", "/account").secure(true).httpOnly(true)
            .sameSitePolicy("strict").expiry(System.currentTimeMillis() / 1000 + 3600).build();
        io.github.webong.ext.web.CookieBundle bundle = io.github.webong.ext.web.CookieWorkflows.bundle(
            cookie, "https://example.com/account", "test");
        workspace.setCookie(bundle, accepted -> {
            if (done) return;
            try {
                check(Boolean.TRUE.equals(accepted), "native cookie accepted");
                check(workspace.cookieHeader(bundle.site).contains("ext_cookie_test=fixture-token"), "native HttpOnly cookie read");
                for (String site : Arrays.asList("http://example.com/account", "https://sub.example.com/account", "https://example.com/other"))
                    check(!workspace.cookieHeader(site).contains("ext_cookie_test="), "native cookie scope");
                io.github.webong.ext.web.Cookie isolated = cookie.toBuilder().partitionKey("https://top.example").build();
                try {
                    workspace.setCookie(new io.github.webong.ext.web.CookieBundle("test", bundle.site, isolated), ignored -> {});
                    check(false, "isolated cookie rejected");
                } catch (io.github.webong.ext.web.CookieException expected) { check(true, "isolated cookie rejected"); }
                io.github.webong.ext.web.Cookie injection = io.github.webong.ext.web.Cookie.builder(
                    "ext_cookie_test", "bad; Domain=other.com", "example.com", "/account").build();
                try {
                    workspace.setCookie(new io.github.webong.ext.web.CookieBundle("test", bundle.site, injection), ignored -> {});
                    check(false, "cookie attribute injection rejected");
                } catch (io.github.webong.ext.web.CookieException expected) { check(true, "cookie attribute injection rejected"); }
                check(workspace.cookieHeader(bundle.site).contains("ext_cookie_test=fixture-token"), "rejected cookie write preserves value");
                // Remove only the test's cookie from Android's shared app cookie store.
                android.webkit.CookieManager.getInstance().setCookie(bundle.site,
                    "ext_cookie_test=; Path=/account; Secure; HttpOnly; Max-Age=0", ignored -> { if (!done) load("/public"); });
            } catch (Exception failure) { fail(failure); }
        });
    }

    private UserscriptRecord record() {
        return new UserscriptRecord("test", "script", "Script", "", Arrays.asList("https://example.com/*"),
            Arrays.asList("https://example.com/private/*"), true,
            "globalThis.runs = (globalThis.runs || 0) + 1;").signed();
    }
    private void load(String path) {
        navigationStarted = true;
        evaluating = false;
        workspace.view().loadDataWithBaseURL("https://example.com" + path,
            "<html><script>window.atStart = window.runs || 0;</script></html>", "text/html", "UTF-8", null);
    }
    private void loaded(String value) {
        if (done) return;
        try {
            if (!expectDocumentStart) {
                check("0".equals(value), "navigation works without AndroidX");
                injectThen(() -> { workspace.close(); finishTests(); });
                return;
            }
            switch (phase++) {
                case 0:
                    check("1".equals(value), "runs before inline page code");
                    injectThen(() -> load("/private/secret")); break;
                case 1:
                    check("0".equals(value), "excluded path");
                    try {
                        route("session", "replay", "{\"records\":[" + record().withRevision("tampered").toJson() + "]}");
                        check(false, "tampered revision rejected");
                    } catch (Exception expected) { check(true, "tampered revision rejected"); }
                    load("/again"); break;
                case 2:
                    check("1".equals(value), "rejected update retains registrations");
                    route("userscript", "disable", "{\"id\":\"script\"}");
                    check(!store.load("test", "script").enabled,"routed disable persisted");
                    route("userscript", "activate", "{}");
                    load("/removed"); break;
                case 3:
                    check("0".equals(value), "disabled script stays removed on navigation");
                    route("userscript", "enable", "{\"id\":\"script\"}");
                    route("session", "replay", "{\"records\":[" + store.load("test", "script").toJson() + "]}");
                    load("/restored"); break;
                case 4:
                    check("1".equals(value), "routed replay restores enabled script");
                    route("userscript", "uninstall", "{\"id\":\"script\"}");
                    check(store.list("test").isEmpty(),"routed uninstall persisted");
                    route("userscript", "activate", "{}");
                    load("/uninstalled"); break;
                default:
                    check("0".equals(value), "uninstalled script removed after activation");
                    check(route("session", "navigate", "{\"url\":\"http://127.0.0.1:1/\"}").status.equals("navigated"),"routed navigation acknowledged");
                    workspace.stop();
                    int[] completions = {0};
                    router.manage("profile", new io.github.webong.ext.web.BrowserManagement.Request("session","inject","{\"source\":\"window.pending = true;\"}"), (r,e) -> {
                        completions[0]++; check(e != null && r == null,"close rejects pending injection");
                    });
                    workspace.close();
                    check(completions[0] == 1,"pending injection completed exactly once on close");
                    try { route("session","targets","{}"); check(false,"closed route rejected"); }
                    catch (Exception expected) { check(true,"closed route rejected"); }
                    try { workspace.reload(); check(false, "closed workspace rejects work"); }
                    catch (IllegalStateException expected) { check(true, "closed workspace rejects work"); }
                    finishTests();
            }
        } catch (Exception failure) { fail(failure); }
    }
    private void check(boolean ok, String name) {
        if (ok) passed++; else failed++;
        Log.i("EXTWORKSPACE", (ok ? "PASS " : "FAIL ") + name);
    }
    private void fail(Exception failure) { Log.e("EXTWORKSPACE", "failure", failure); failed++; finishTests(); }
    private void finishTests() {
        if (workspace != null) {
            workspace.close();
            try { workspace.cookieHeader("https://example.com/"); check(false, "closed workspace rejects cookie access"); }
            catch (IllegalStateException expected) { check(true, "closed workspace rejects cookie access"); }
            catch (Exception unexpected) { check(false, "closed workspace cookie error"); }
        }
        if (bundles == null && failed == 0) {
            bundles = new BundleChecks(this, this::check, this::reportDone);
            bundles.start();
        } else reportDone();
    }
    private void reportDone() { done = true; Log.i("EXTWORKSPACE", "DONE passed=" + passed + " failed=" + failed); }
    @Override public void onDestroy() { done = true; if (bundles != null) bundles.close(); if (workspace != null) workspace.close(); super.onDestroy(); }
}
