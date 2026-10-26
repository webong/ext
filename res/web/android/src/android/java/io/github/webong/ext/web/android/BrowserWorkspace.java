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
import io.github.webong.ext.web.CookieBundle;
import io.github.webong.ext.web.CookieException;
import io.github.webong.ext.web.Site;
import io.github.webong.ext.web.SiteException;
import io.github.webong.ext.web.UserscriptException;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.ScriptExecutionCapability;
import io.github.webong.ext.web.WebViewCapabilities;
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
        public final long navigationGeneration;
        private State(WebView view, boolean loading, String error, long navigationGeneration) {
            url = view.getUrl() == null ? "" : view.getUrl();
            title = view.getTitle() == null ? "" : view.getTitle();
            this.loading = loading; this.error = error;
            progress = view.getProgress() / 100.0;
            canGoBack = view.canGoBack(); canGoForward = view.canGoForward();
            this.navigationGeneration = navigationGeneration;
        }
    }

    public interface Listener {
        default void onStateChange(State state) {}
        /** UI-thread store reconciliation result; false retains prior registrations.
         * No script source or underlying error is exposed. */
        default void onUserscriptsReconciled(boolean success) {}
        /** Completed main-page visits; the host chooses storage and retention. */
        default void onVisit(State state) {}
        /** Denied non-HTTP main-frame navigation with a user gesture; never launched automatically. */
        default void onExternalURL(Uri url) {}
    }

    private final android.os.Handler reconciliationHandler = new android.os.Handler(Looper.getMainLooper());
    private static final class LiveScripts {
        final Object token;
        final io.github.webong.ext.web.UserscriptStore store;
        final String id;
        final io.github.webong.ext.web.UserscriptStore.Observation observation;
        List<UserscriptRecord> records;
        boolean pending;
        LiveScripts(Object token, io.github.webong.ext.web.UserscriptStore store, String id,
                io.github.webong.ext.web.UserscriptStore.Observation observation, List<UserscriptRecord> records) {
            this.token = token; this.store = store; this.id = id; this.observation = observation; this.records = records;
        }
    }
    private LiveScripts liveScripts;
    private final WebView view;
    private final String target;
    private final DocumentStart documentStart = new DocumentStart();
    private List<Object> scripts = new ArrayList<>();
    private Listener listener;
    private boolean closed, loading, failed;
    private String error;
    private long injectionID, navigationGeneration;
    private static final class PendingInjection {
        final long generation;
        final android.webkit.ValueCallback<Boolean> completion;
        PendingInjection(long generation, android.webkit.ValueCallback<Boolean> completion) {
            this.generation = generation; this.completion = completion;
        }
    }
    private final java.util.Map<Long, PendingInjection> injections = new java.util.LinkedHashMap<>();

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
    public State state() { requireOpen(); return new State(view, loading, error, navigationGeneration); }
    /** Native request header, including HttpOnly cookies. No domain/expiry metadata is
     * available through CookieManager; do not treat this as a portable cookie export.
     * Android's app-level WebView cookie store is shared across workspaces. */
    public String cookieHeader(String site) throws SiteException {
        requireOpen();
        Site.parse(site);
        String header = android.webkit.CookieManager.getInstance().getCookie(site);
        return header == null ? "" : header;
    }

    /** Validates before mutation. The callback reports the native write result on the UI
     * thread, and may complete after close(). Does not change app cookie acceptance policy. */
    public void setCookie(CookieBundle bundle, android.webkit.ValueCallback<Boolean> completion)
            throws CookieException {
        requireOpen();
        if (completion == null) throw new IllegalArgumentException("cookie completion is required");
        String header = WorkspaceCookies.header(bundle);
        android.webkit.CookieManager.getInstance().setCookie(bundle.site, header, completion);
    }

    public boolean supportsDocumentStartScripts() { requireOpen(); return documentStart.available(); }
    public WebViewCapabilities scriptCapabilities() {
        requireOpen();
        ScriptExecutionCapability current = new ScriptExecutionCapability(true,
                java.util.Collections.singletonList("main"), java.util.Collections.singletonList("now"),
                java.util.Collections.singletonList("page"), true);
        ScriptExecutionCapability future = documentStart.available()
                ? new ScriptExecutionCapability(true, java.util.Collections.singletonList("all"),
                    java.util.Collections.singletonList("document-start"), java.util.Collections.singletonList("page"), false)
                : new ScriptExecutionCapability(false, java.util.Collections.<String>emptyList(),
                    java.util.Collections.<String>emptyList(), java.util.Collections.<String>emptyList(), false);
        return new WebViewCapabilities(current, future);
    }
    public void navigate(String url) throws SiteException {
        requireOpen(); Site.parse(url); beginNavigation(); view.loadUrl(url);
    }
    public void back() { requireOpen(); if (view.canGoBack()) { beginNavigation(); view.goBack(); } }
    public void forward() { requireOpen(); if (view.canGoForward()) { beginNavigation(); view.goForward(); } }
    public void reload() { requireOpen(); beginNavigation(); view.reload(); }
    public void stop() { requireOpen(); view.stopLoading(); loading = false; emit(); }

    /** Replaces registrations for future documents; cannot undo effects on the current page. */
    public void replaceUserscripts(List<UserscriptRecord> records) throws UserscriptException {
        requireOpen();
        applyUserscripts(records);
        stopUserscriptReconciliation();
    }
    private void applyUserscripts(List<UserscriptRecord> records) throws UserscriptException {
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

    /** Registers and observes enabled scripts. Empty id selects all. Failure preserves
     * the previous binding/registrations. Subsequent disable/removal clears the selected script. */
    public int activateUserscripts(io.github.webong.ext.web.UserscriptStore store, String id)
            throws java.io.IOException, UserscriptException {
        requireOpen(); Objects.requireNonNull(store); Objects.requireNonNull(id);
        Object token = new Object();
        java.lang.ref.WeakReference<BrowserWorkspace> weak = new java.lang.ref.WeakReference<>(this);
        android.os.Handler handler = reconciliationHandler;
        io.github.webong.ext.web.UserscriptStore.Observation observation = store.observe(target, () ->
            handler.post(() -> { BrowserWorkspace workspace = weak.get(); if (workspace != null) workspace.scheduleReconciliation(token); }));
        boolean complete = false;
        try {
            List<UserscriptRecord> records = selectedRecords(store, id);
            if (!id.isEmpty() && records.isEmpty()) throw new IllegalArgumentException("selected userscript is missing or disabled");
            applyUserscripts(records);
            // Do not remove the new observation's already-queued change notification.
            if (liveScripts != null) liveScripts.observation.close();
            liveScripts = new LiveScripts(token, store, id, observation, records);
            complete = true; return records.size();
        } finally { if (!complete) observation.close(); }
    }
    public boolean isReconcilingUserscripts() { requireUI(); return liveScripts != null; }
    /** Retains registrations while detaching from the store. UI thread only. */
    public void stopUserscriptReconciliation() {
        requireUI();
        if (liveScripts != null) liveScripts.observation.close();
        liveScripts = null;
        reconciliationHandler.removeCallbacksAndMessages(null);
    }
    private List<UserscriptRecord> selectedRecords(io.github.webong.ext.web.UserscriptStore store, String id)
            throws java.io.IOException, UserscriptException {
        List<UserscriptRecord> records = new ArrayList<>();
        for (UserscriptRecord record : store.list(target))
            if (record.enabled && (id.isEmpty() || record.id.equals(id))) records.add(record);
        return records;
    }
    private void scheduleReconciliation(Object token) {
        if (closed || liveScripts == null || liveScripts.token != token || liveScripts.pending) return;
        liveScripts.pending = true;
        java.lang.ref.WeakReference<BrowserWorkspace> weak = new java.lang.ref.WeakReference<>(this);
        reconciliationHandler.post(() -> { BrowserWorkspace workspace = weak.get(); if (workspace != null) workspace.reconcile(token); });
    }
    private void reconcile(Object token) {
        if (closed || liveScripts == null || liveScripts.token != token) return;
        LiveScripts live = liveScripts; live.pending = false;
        try {
            List<UserscriptRecord> records = selectedRecords(live.store, live.id);
            if (!records.equals(live.records)) { applyUserscripts(records); live.records = records; }
        } catch (Exception failure) { listener.onUserscriptsReconciled(false); return; }
        listener.onUserscriptsReconciled(true);
    }

    @Override public void close() {
        requireUI();
        if (closed) return;
        closed = true;
        stopUserscriptReconciliation();
        view.stopLoading();
        for (Object handler : scripts) documentStart.remove(handler);
        scripts.clear();
        view.setWebChromeClient(null);
        view.setWebViewClient(new WebViewClient());
        if (view.getParent() instanceof ViewGroup) ((ViewGroup) view.getParent()).removeView(view);
        view.destroy();
        listener = null;
        List<PendingInjection> pending = new ArrayList<>(injections.values());
        injections.clear();
        for (PendingInjection injection : pending) injection.completion.onReceiveValue(false);
    }

    private void emit() { if (!closed) listener.onStateChange(new State(view, loading, error, navigationGeneration)); }
    String managementTarget() { requireOpen(); return target; }
    void checkOpen() { requireOpen(); }
    /** Executes in the current main frame's page world. The callback reports synchronous
     * evaluation success; JavaScript promises are not awaited. */
    public void inject(String source, android.webkit.ValueCallback<Boolean> completion) {
        requireOpen();
        if (completion == null) throw new IllegalArgumentException("injection completion is required");
        io.github.webong.ext.web.WorkspaceInput.validateSource(source);
        String script = "(function(){try{(0,eval)(" + io.github.webong.ext.web.WorkspaceInput.quote(source)
            + ");return true;}catch(_){return false;}})()";
        long id = ++injectionID;
        long generation = navigationGeneration;
        injections.put(id, new PendingInjection(generation, completion));
        try {
            view.evaluateJavascript(script, value -> {
                PendingInjection pending = injections.remove(id);
                if (pending != null) pending.completion.onReceiveValue(!closed
                        && pending.generation == navigationGeneration && "true".equals(value));
            });
        } catch (RuntimeException failure) { injections.remove(id); throw failure; }
    }
    private void beginNavigation() {
        navigationGeneration++;
        List<PendingInjection> pending = new ArrayList<>(injections.values());
        injections.clear();
        for (PendingInjection injection : pending) injection.completion.onReceiveValue(false);
    }
    private void requireOpen() { requireUI(); if (closed) throw new IllegalStateException("workspace is closed"); }
    private static void requireUI() {
        if (Looper.myLooper() != Looper.getMainLooper()) throw new IllegalStateException("workspace requires the UI thread");
    }
    private static boolean http(Uri url) {
        return "http".equalsIgnoreCase(url.getScheme()) || "https".equalsIgnoreCase(url.getScheme());
    }

    private final class Client extends WebViewClient {
        @Override public void onPageStarted(WebView webView, String url, Bitmap icon) {
            beginNavigation(); loading = true; failed = false; error = null; emit();
        }
        @Override public void onPageFinished(WebView webView, String url) {
            loading = false; emit();
            if (!closed && !failed && url != null && http(Uri.parse(url))) {
                listener.onVisit(new State(view, loading, error, navigationGeneration));
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
