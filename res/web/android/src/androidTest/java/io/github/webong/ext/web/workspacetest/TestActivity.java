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
            if (expectDocumentStart) {
                workspace.replaceUserscripts(Collections.singletonList(record()));
            } else {
                try {
                    workspace.replaceUserscripts(Collections.singletonList(record()));
                    check(false, "unsupported registration rejected");
                } catch (UnsupportedOperationException expected) { check(true, "unsupported registration rejected"); }
                workspace.replaceUserscripts(Collections.emptyList());
            }
            testCookies();
            new Handler(Looper.getMainLooper()).postDelayed(() -> {
                if (!done) { check(false, "test timeout"); finishTests(); }
            }, 30000);
        } catch (Exception failure) { fail(failure); }
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
                workspace.close();
                finishTests();
                return;
            }
            switch (phase++) {
                case 0:
                    check("1".equals(value), "runs before inline page code");
                    load("/private/secret"); break;
                case 1:
                    check("0".equals(value), "excluded path");
                    try {
                        workspace.replaceUserscripts(Collections.singletonList(record().withRevision("tampered")));
                        check(false, "tampered revision rejected");
                    } catch (Exception expected) { check(true, "tampered revision rejected"); }
                    load("/again"); break;
                case 2:
                    check("1".equals(value), "rejected update retains registrations");
                    workspace.replaceUserscripts(Collections.emptyList());
                    load("/removed"); break;
                default:
                    check("0".equals(value), "removed script stays removed on navigation");
                    workspace.close();
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
        done = true; Log.i("EXTWORKSPACE", "DONE passed=" + passed + " failed=" + failed); }
    @Override public void onDestroy() { done = true; if (workspace != null) workspace.close(); super.onDestroy(); }
}
