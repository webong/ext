package io.github.webong.ext.web.android;

import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.webkit.CookieManager;
import android.webkit.ValueCallback;
import android.webkit.WebView;
import io.github.webong.ext.web.CookieBundle;
import io.github.webong.ext.web.CookieException;
import io.github.webong.ext.web.ScriptExecutionCapability;
import io.github.webong.ext.web.Site;
import io.github.webong.ext.web.SiteException;
import io.github.webong.ext.web.UserscriptException;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.WebViewCapabilities;
import io.github.webong.ext.web.WebViewUserscripts;
import io.github.webong.ext.web.WorkspaceInput;
import io.github.webong.ext.web.WorkspaceScripts;
import java.lang.ref.WeakReference;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/** Script and cookie operations on a caller-owned view. All access uses the UI thread. */
public final class AttachedWebView implements AutoCloseable {
    private final WeakReference<WebView> view;
    private final String target;
    private final DocumentStart documentStart = new DocumentStart();
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Map<Long, Injection> injections = new LinkedHashMap<>();
    private List<Object> scripts = new ArrayList<>();
    private CookieManager cookieStore;
    private long injectionID;

    private static final class Injection {
        final ValueCallback<Boolean> completion;
        final Runnable timeout;

        Injection(ValueCallback<Boolean> completion, Runnable timeout) {
            this.completion = completion;
            this.timeout = timeout;
        }
    }

    /** Attaches with cookie operations disabled. */
    public AttachedWebView(WebView view, String target) {
        this(view, target, null);
    }

    /**
     * The caller supplies the CookieManager for the view's chosen profile. Passing
     * null disables cookie operations. Use the app-wide manager only for a view
     * configured for that shared store. The attachment never sets a profile.
     */
    public AttachedWebView(WebView view, String target, CookieManager cookieStore) {
        requireUI();
        Objects.requireNonNull(view, "view");
        if (Build.VERSION.SDK_INT >= 28 && view.getWebViewLooper() != Looper.getMainLooper()) {
            throw new IllegalArgumentException("view must use the main looper");
        }
        this.view = new WeakReference<>(view);
        this.target = Objects.requireNonNull(target, "target");
        this.cookieStore = cookieStore;
    }

    public boolean supportsDocumentStartScripts() {
        return requireView().getSettings().getJavaScriptEnabled() && documentStart.available();
    }

    public WebViewCapabilities scriptCapabilities() {
        boolean current = requireView().getSettings().getJavaScriptEnabled();
        boolean future = current && documentStart.available();
        return new WebViewCapabilities(capability(current, "main", "now", current),
            capability(future, "all", "document-start", false));
    }

    /** Exact v1 profiles supported by the installed native hook. */
    public List<WebViewUserscripts.Profile> userscriptProfiles() {
        if (!supportsDocumentStartScripts()) return Collections.emptyList();
        return Collections.unmodifiableList(Arrays.asList(
            new WebViewUserscripts.Profile(WebViewUserscripts.Timing.DOCUMENT_START, WebViewUserscripts.Frames.MAIN),
            new WebViewUserscripts.Profile(WebViewUserscripts.Timing.DOCUMENT_START, WebViewUserscripts.Frames.ALL)));
    }

    private static ScriptExecutionCapability capability(boolean supported, String frame, String timing, boolean stale) {
        List<String> empty = Collections.emptyList();
        return new ScriptExecutionCapability(supported,
            supported ? Collections.singletonList(frame) : empty,
            supported ? Collections.singletonList(timing) : empty,
            supported ? Collections.singletonList("page") : empty, stale);
    }

    /** Replaces this attachment's future-document scripts using native handles. */
    public void replaceUserscripts(List<UserscriptRecord> records) throws UserscriptException {
        requireView();
        List<String> sources = WorkspaceScripts.prepare(records, target);
        if (!sources.isEmpty() && !supportsDocumentStartScripts()) {
            throw new UnsupportedOperationException("Document-start scripts require enabled JavaScript and AndroidX WebKit support");
        }
        replaceSources(sources);
    }

    /** Replaces this attachment's future scripts with reviewed v1 records. */
    public void replaceProfiledUserscripts(List<UserscriptRecord> records) throws UserscriptException {
        List<WebViewUserscripts.Registration> registrations = WebViewUserscripts.prepare(records, target, userscriptProfiles());
        List<String> sources = new ArrayList<>();
        for (WebViewUserscripts.Registration registration : registrations) sources.add(registration.source);
        replaceSources(sources);
    }

    private void replaceSources(List<String> sources) {
        WebView selected = requireView();
        List<Object> next = new ArrayList<>();
        try {
            for (String source : sources) next.add(documentStart.add(selected, source));
            removeScripts(scripts);
        } catch (RuntimeException failure) {
            try {
                removeScripts(next);
            } catch (RuntimeException cleanup) {
                failure.addSuppressed(cleanup);
                scripts.addAll(next);
            }
            throw failure;
        }
        scripts = next;
    }

    private void removeScripts(List<Object> registrations) {
        RuntimeException failure = null;
        Iterator<Object> remaining = registrations.iterator();
        while (remaining.hasNext()) {
            Object registration = remaining.next();
            try {
                documentStart.remove(registration);
                remaining.remove();
            } catch (RuntimeException error) {
                if (failure == null) failure = error;
                else failure.addSuppressed(error);
            }
        }
        if (failure != null) throw failure;
    }

    /** Native request header, including HttpOnly cookies, from the chosen store. */
    public String cookieHeader(String site) throws SiteException {
        CookieManager store = requireCookieStore();
        Site.parse(site);
        String header = store.getCookie(site);
        return header == null ? "" : header;
    }

    /** Accepted cookie writes may finish after the attachment closes. */
    public void setCookie(CookieBundle bundle, ValueCallback<Boolean> completion) throws CookieException {
        CookieManager store = requireCookieStore();
        if (completion == null) throw new IllegalArgumentException("cookie completion is required");
        store.setCookie(bundle.site, WorkspaceCookies.header(bundle), completion);
    }

    /** Executes reviewed source in the current main frame's page world. The
     * callback reports synchronous completion, including a false result on timeout. */
    public void inject(String source, ValueCallback<Boolean> completion) {
        WebView selected = requireView();
        if (!selected.getSettings().getJavaScriptEnabled()) {
            throw new UnsupportedOperationException("JavaScript is disabled for this WebView");
        }
        if (completion == null) throw new IllegalArgumentException("injection completion is required");
        WorkspaceInput.validateSource(source);
        if (injections.size() >= 64) throw new IllegalStateException("too many pending WebView evaluations");
        String script = "(function(){try{(0,eval)(" + WorkspaceInput.quote(source)
            + ");return true;}catch(_){return false;}})()";
        long id = ++injectionID;
        String url = selected.getUrl();
        Runnable timeout = () -> finish(id, false);
        injections.put(id, new Injection(completion, timeout));
        handler.postDelayed(timeout, 30_000);
        WeakReference<AttachedWebView> owner = new WeakReference<>(this);
        try {
            selected.evaluateJavascript(script, value -> {
                AttachedWebView attachment = owner.get();
                if (attachment != null) attachment.finishEvaluation(id, url, value);
            });
        } catch (RuntimeException failure) {
            injections.remove(id);
            handler.removeCallbacks(timeout);
            throw failure;
        }
    }

    private void finishEvaluation(long id, String url, String result) {
        if (!injections.containsKey(id)) return;
        WebView current = view.get();
        finish(id, current != null && Objects.equals(url, current.getUrl()) && "true".equals(result));
    }

    private void finish(long id, boolean success) {
        Injection pending = injections.remove(id);
        if (pending == null) return;
        handler.removeCallbacks(pending.timeout);
        pending.completion.onReceiveValue(success);
    }

    /**
     * Forward main-frame navigation starts and renderer invalidation here,
     * including same-URL reloads. Submitted JavaScript cannot be recalled.
     */
    public void invalidateNavigation() {
        requireUI();
        List<Injection> pending = new ArrayList<>(injections.values());
        injections.clear();
        handler.removeCallbacksAndMessages(null);
        for (Injection injection : pending) injection.completion.onReceiveValue(false);
    }

    /** Call before destroying the view. Leaves the view, clients and settings in place. */
    @Override public void close() {
        requireUI();
        view.clear();
        cookieStore = null;
        try {
            removeScripts(scripts);
        } finally {
            invalidateNavigation();
        }
    }

    private WebView requireView() {
        requireUI();
        WebView selected = view.get();
        if (selected == null) throw new IllegalStateException("WebView attachment is closed");
        return selected;
    }

    private CookieManager requireCookieStore() {
        requireView();
        if (cookieStore == null) throw new UnsupportedOperationException("no cookie store was supplied for this WebView");
        return cookieStore;
    }

    private static void requireUI() {
        if (Looper.myLooper() != Looper.getMainLooper()) throw new IllegalStateException("WebView attachment requires the UI thread");
    }
}
