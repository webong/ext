package io.github.webong.ext.web.workspacetest;

import android.app.Activity;
import android.graphics.Bitmap;
import android.os.Build;
import android.webkit.CookieManager;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.LinearLayout;
import io.github.webong.ext.web.Cookie;
import io.github.webong.ext.web.CookieBundle;
import io.github.webong.ext.web.CookieWorkflows;
import io.github.webong.ext.web.UserscriptException;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.WebViewUserscripts;
import io.github.webong.ext.web.android.AttachedWebView;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Collections;
import java.util.function.BiConsumer;
import java.util.function.Consumer;

final class AttachedViewChecks {
    private final Activity activity;
    private final boolean documentStart;
    private final BiConsumer<Boolean, String> check;
    private final Consumer<Exception> failed;
    private final Runnable finished;
    private WebView selected, other;
    private HostClient hostClient, otherClient;
    private WebChromeClient hostChrome;
    private AttachedWebView attached;
    private LinearLayout container;

    AttachedViewChecks(Activity activity, boolean documentStart, BiConsumer<Boolean, String> check,
            Consumer<Exception> failed, Runnable finished) {
        this.activity = activity;
        this.documentStart = documentStart;
        this.check = check;
        this.failed = failed;
        this.finished = finished;
    }

    void start() {
        try {
            selected = new WebView(activity);
            other = new WebView(activity);
            // The attachment must leave disabled JavaScript disabled.
            AttachedWebView disabled = new AttachedWebView(selected, "attached");
            check.accept(!disabled.scriptCapabilities().currentPage.supported, "attached capability reflects host settings");
            try {
                disabled.inject("void 0", ignored -> {});
                check.accept(false, "disabled JavaScript refused");
            } catch (UnsupportedOperationException expected) {
                check.accept(true, "disabled JavaScript refused");
            }
            try {
                disabled.cookieHeader("https://example.com/");
                check.accept(false, "attached cookies require a selected store");
            } catch (UnsupportedOperationException expected) {
                check.accept(true, "attached cookies require a selected store");
            }
            disabled.close();
            selected.getSettings().setJavaScriptEnabled(true);
            other.getSettings().setJavaScriptEnabled(true);
            hostClient = new HostClient();
            otherClient = new HostClient();
            hostChrome = new WebChromeClient();
            selected.setWebViewClient(hostClient);
            selected.setWebChromeClient(hostChrome);
            other.setWebViewClient(otherClient);
            container = new LinearLayout(activity);
            container.setOrientation(LinearLayout.VERTICAL);
            container.addView(selected);
            container.addView(other);
            activity.setContentView(container);
            CookieManager store = CookieManager.getInstance();
            attached = new AttachedWebView(selected, "attached", store);
            hostClient.attachment = attached;
            check.accept(attached.supportsDocumentStartScripts() == documentStart, "attached document-start capability");
            UserscriptRecord record = profile("script", "document-start", true, 1);
            if (documentStart) {
                // Install the host's registration independently of Ext.
                Class.forName("androidx.webkit.WebViewCompat").getMethod("addDocumentStartJavaScript",
                    WebView.class, String.class, java.util.Set.class)
                    .invoke(null, selected, "window.hostValue = 10;", Collections.singleton("*"));
                attached.replaceProfiledUserscripts(Arrays.asList(record,
                    profile("all-frames", "document-start", false, 2)));
            } else {
                try {
                    attached.replaceProfiledUserscripts(Collections.singletonList(record));
                    check.accept(false, "missing document-start denied");
                } catch (UserscriptException expected) {
                    check.accept(expected.code().equals("unsupported_profile"), "missing document-start denied");
                }
            }
            try {
                attached.replaceProfiledUserscripts(Arrays.asList(profile("script", "document-start", true, 99),
                    profile("late", "document-end", false, 100)));
                check.accept(false, "unsupported document-end batch denied");
            } catch (UserscriptException expected) {
                check.accept(expected.code().equals("unsupported_profile"), "unsupported document-end batch denied");
            }
            String site = "https://example.com/attached";
            CookieBundle bundle = CookieWorkflows.bundle(Cookie.builder("ext_attached_test", "fixture", "example.com", "/attached")
                .secure(true).httpOnly(true).build(), site, "attached");
            attached.setCookie(bundle, accepted -> attempt(() -> {
                check.accept(Boolean.TRUE.equals(accepted) && attached.cookieHeader(site).contains("ext_attached_test=fixture"),
                    "attached cookie uses selected store");
                check.accept(!attached.cookieHeader("https://example.com/other").contains("ext_attached_test="),
                    "attached cookie uses request path");
                store.setCookie(site, "ext_attached_test=; Path=/attached; Secure; HttpOnly; Max-Age=0", ignored -> loadSelected());
            }));
        } catch (Exception failure) {
            failed.accept(failure);
        }
    }

    private static UserscriptRecord profile(String id, String timing, boolean mainFrame, int value) {
        UserscriptRecord record = new UserscriptRecord("attached", id, "Script", "",
            null, null, true, "// ==UserScript==\n// @run-at " + timing
                + (mainFrame ? "\n// @noframes" : "")
                + "\n// @include https://example.com/*\n// ==/UserScript==\n"
                + (mainFrame ? "window.extMainValue" : "window.extAllValue") + " = " + value + ";");
        return record.withRevision(WebViewUserscripts.revision(record));
    }

    private static final String START_REPORT =
        "<script>window.atStart = (window.hostValue || 0) + (window.extMainValue || 0) + (window.extAllValue || 0);</script>";

    private void loadSelected() {
        load(selected, hostClient, "/attached", value -> {
            check.accept((documentStart ? "13" : "0").equals(value),
                documentStart ? "approved start profiles survived rejected batch" : "page ran without document-start scripts");
            selected.evaluateJavascript("window.frames[0].atStart", child -> check.accept(
                (documentStart ? "12" : "0").equals(child), "child ran only the all-frame profile"));
            int[] completions = {0};
            attached.inject("window.pending = true;", accepted -> {
                completions[0]++;
                check.accept(!accepted, "stale attached evaluation rejected");
            });
            attached.invalidateNavigation();
            attached.inject("window.evaluated = 42;", accepted -> {
                check.accept(accepted && completions[0] == 1, "selected view evaluated once");
                selected.evaluateJavascript("window.evaluated", result -> {
                    check.accept("42".equals(result), "attached evaluation changed selected page");
                    load(other, otherClient, "/other", otherValue -> {
                        check.accept("0".equals(otherValue), "registrations are isolated to the selected view");
                        detachView();
                    });
                });
            });
        });
    }

    private void detachView() {
        attached.close();
        hostClient.attachment = null;
        if (Build.VERSION.SDK_INT >= 26) {
            check.accept(selected.getWebViewClient() == hostClient && selected.getWebChromeClient() == hostChrome,
                "attachment preserved host clients");
        }
        load(selected, hostClient, "/detached", value -> {
            check.accept((documentStart ? "10" : "0").equals(value), "detached view and host scripts still work");
            close();
            try {
                attached.inject("void 0", ignored -> {});
                check.accept(false, "closed attachment rejected after owner destroys view");
            } catch (IllegalStateException expected) {
                check.accept(true, "closed attachment rejected after owner destroys view");
            }
            finished.run();
        });
    }

    private static void load(WebView view, HostClient client, String path, Consumer<String> completed) {
        client.finished = () -> view.evaluateJavascript("window.atStart", completed::accept);
        view.loadDataWithBaseURL("https://example.com" + path,
            START_REPORT + "<iframe src='https://example.com/child'></iframe>",
            "text/html", "UTF-8", null);
    }

    void close() {
        if (attached != null) attached.close();
        if (container != null) container.removeAllViews();
        if (selected != null) selected.destroy();
        if (other != null) other.destroy();
        selected = null;
        other = null;
    }

    private interface Checked { void run() throws Exception; }

    private void attempt(Checked action) {
        try { action.run(); }
        catch (Exception failure) { failed.accept(failure); }
    }

    private static final class HostClient extends WebViewClient {
        AttachedWebView attachment;
        Runnable finished;

        @Override public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
            if (!"https://example.com/child".equals(request.getUrl().toString())) return null;
            byte[] html = START_REPORT.getBytes(StandardCharsets.UTF_8);
            return new WebResourceResponse("text/html", "UTF-8", new ByteArrayInputStream(html));
        }

        @Override public void onPageStarted(WebView view, String url, Bitmap icon) {
            if (attachment != null) attachment.invalidateNavigation();
        }

        @Override public void onPageFinished(WebView view, String url) {
            Runnable action = finished;
            finished = null;
            if (action != null) action.run();
        }
    }
}
