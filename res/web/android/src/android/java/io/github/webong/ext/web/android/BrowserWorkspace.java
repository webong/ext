package io.github.webong.ext.web.android;

import android.annotation.SuppressLint;
import android.content.Context;
import android.graphics.Bitmap;
import android.net.Uri;
import android.os.Looper;
import android.view.ViewGroup;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import io.github.webong.ext.web.Site;
import io.github.webong.ext.web.SiteException;
import io.github.webong.ext.web.UserscriptException;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.WorkspaceScripts;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;

/**
 * A host-owned Android WebView page (API 24+). All access and callbacks use the UI thread.
 * Embed view() in your UI; do not replace its clients. Call close() when the owner is destroyed.
 * WebView's app-level cookie/storage profile is shared; this is not an incognito profile.
 */
public final class BrowserWorkspace implements AutoCloseable {
    public static final class State {
        public final String url, title, error;
        public final boolean loading, canGoBack, canGoForward;
        public final double progress;
        private State(WebView view, boolean loading, String error) {
            url = view.getUrl() == null ? "" : view.getUrl();
            title = view.getTitle() == null ? "" : view.getTitle();
            this.loading = loading; this.error = error;
            progress = view.getProgress() / 100.0;
            canGoBack = view.canGoBack(); canGoForward = view.canGoForward();
        }
    }

    public interface Listener {
        default void onStateChange(State state) {}
        /** Completed main-page visits; the host chooses storage and retention. */
        default void onVisit(State state) {}
        /** Denied non-HTTP main-frame navigation with a user gesture; never launched automatically. */
        default void onExternalURL(Uri url) {}
    }

    private final WebView view;
    private final String target;
    private final DocumentStart documentStart = new DocumentStart();
    private List<Object> scripts = new ArrayList<>();
    private Listener listener;
    private boolean closed, loading, failed;
    private String error;

    @SuppressLint("SetJavaScriptEnabled")
    public BrowserWorkspace(Context context, String target, Listener listener) {
        requireUI();
        this.target = Objects.requireNonNull(target);
        this.listener = Objects.requireNonNull(listener);
        view = new WebView(context);
        view.getSettings().setJavaScriptEnabled(true);
        view.getSettings().setDomStorageEnabled(true);
        view.getSettings().setAllowFileAccess(false);
        view.getSettings().setAllowContentAccess(false);
        view.getSettings().setJavaScriptCanOpenWindowsAutomatically(false);
        view.getSettings().setSupportMultipleWindows(false);
        view.getSettings().setMediaPlaybackRequiresUserGesture(true);
        view.setWebViewClient(new Client());
        view.setWebChromeClient(new WebChromeClient() {
            @Override public void onProgressChanged(WebView webView, int progress) { emit(); }
            @Override public void onReceivedTitle(WebView webView, String title) { emit(); }
        });
    }

    public WebView view() { requireOpen(); return view; }
    public State state() { requireOpen(); return new State(view, loading, error); }
    public boolean supportsDocumentStartScripts() { requireOpen(); return documentStart.available(); }
    public void navigate(String url) throws SiteException {
        requireOpen(); Site.parse(url); view.loadUrl(url);
    }
    public void back() { requireOpen(); if (view.canGoBack()) view.goBack(); }
    public void forward() { requireOpen(); if (view.canGoForward()) view.goForward(); }
    public void reload() { requireOpen(); view.reload(); }
    public void stop() { requireOpen(); view.stopLoading(); loading = false; emit(); }

    /** Replaces registrations for future documents; cannot undo effects on the current page. */
    public void replaceUserscripts(List<UserscriptRecord> records) throws UserscriptException {
        requireOpen();
        List<String> sources = WorkspaceScripts.prepare(records, target);
        if (!sources.isEmpty() && !documentStart.available()) {
            throw new UnsupportedOperationException("Document-start scripts require AndroidX WebKit and WebView support");
        }
        List<Object> next = new ArrayList<>();
        try {
            for (String source : sources) next.add(documentStart.add(view, source));
        } catch (RuntimeException failure) {
            for (Object handler : next) documentStart.remove(handler);
            throw failure;
        }
        for (Object handler : scripts) documentStart.remove(handler);
        scripts = next;
    }

    @Override public void close() {
        requireUI();
        if (closed) return;
        closed = true;
        view.stopLoading();
        for (Object handler : scripts) documentStart.remove(handler);
        scripts.clear();
        view.setWebChromeClient(null);
        view.setWebViewClient(new WebViewClient());
        if (view.getParent() instanceof ViewGroup) ((ViewGroup) view.getParent()).removeView(view);
        view.destroy();
        listener = null;
    }

    private void emit() { if (!closed) listener.onStateChange(new State(view, loading, error)); }
    private void requireOpen() { requireUI(); if (closed) throw new IllegalStateException("workspace is closed"); }
    private static void requireUI() {
        if (Looper.myLooper() != Looper.getMainLooper()) throw new IllegalStateException("workspace requires the UI thread");
    }
    private static boolean http(Uri url) {
        return "http".equalsIgnoreCase(url.getScheme()) || "https".equalsIgnoreCase(url.getScheme());
    }

    private final class Client extends WebViewClient {
        @Override public void onPageStarted(WebView webView, String url, Bitmap icon) {
            loading = true; failed = false; error = null; emit();
        }
        @Override public void onPageFinished(WebView webView, String url) {
            loading = false; emit();
            if (!closed && !failed && url != null && http(Uri.parse(url))) {
                listener.onVisit(new State(view, loading, error));
            }
        }
        @Override public void onReceivedError(WebView webView, WebResourceRequest request, WebResourceError failure) {
            if (request.isForMainFrame()) {
                failed = true; loading = false; error = failure.getDescription().toString(); emit();
            }
        }
        @Override public void onReceivedHttpError(WebView webView, WebResourceRequest request, WebResourceResponse response) {
            if (request.isForMainFrame()) {
                failed = true; error = "HTTP " + response.getStatusCode(); emit();
            }
        }
        @Override public boolean shouldOverrideUrlLoading(WebView webView, WebResourceRequest request) {
            if (closed) return true;
            Uri url = request.getUrl();
            if (http(url) || "about:blank".equals(url.toString())) return false;
            if (request.isForMainFrame() && request.hasGesture()) listener.onExternalURL(url);
            return true;
        }
    }
}
