package io.github.webong.ext.web.android;

import android.content.Context;
import android.os.Handler;
import android.os.Build;
import android.view.ViewGroup;
import android.webkit.RenderProcessGoneDetail;
import android.os.Looper;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceError;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import io.github.webong.ext.web.BundlePolicy;
import java.io.File;
import java.io.IOException;
import java.net.URI;
import java.util.concurrent.CompletableFuture;

/** Runs a local bundle in its own view. Construct, attach view(), and run on the UI thread.
 * Resources use CSP; navigation stays on loopback. Android WebView storage is app-wide,
 * so this is not an isolated browser profile or a capability sandbox. No AndroidX required.
 */
public final class BundleRunner implements AutoCloseable {
    public static final class Options {
        public final File root;
        public String entry = "index.html";
        public BundlePolicy policy = new BundlePolicy();
        public boolean crossOriginIsolation;
        public long timeoutMillis, startupTimeoutMillis = 30000, heartbeatTimeoutMillis = 15000;
        public Options(File root) { this.root = root; }
    }
    public static final class Result {
        public final String status, reason, url;
        public final int exitCode;
        Result(String status, int code, String reason, String url) {
            this.status = status; this.exitCode = code; this.reason = reason; this.url = url;
        }
    }
    public interface Listener {
        default void onReady() {}
        default void onConsole(String level, String text) {}
        default void onMessage(String text) {}
    }
    private final Handler main = new Handler(Looper.getMainLooper());
    private final WebView view;
    private final BundleServer server;
    private final CompletableFuture<Result> result = new CompletableFuture<>();
    private boolean started, ended, rendererGone;
    private String origin;

    public BundleRunner(Context context, Options options, Listener listener) throws IOException {
        requireMain();
        Listener events = listener == null ? new Listener() {} : listener;
        server = new BundleServer(options, new BundleServer.Events() {
            public void ready() { main.post(events::onReady); }
            public void console(String level, String text) { main.post(() -> events.onConsole(level, text)); }
            public void message(String text) { main.post(() -> events.onMessage(text)); }
            public void finished(Result value) { main.post(() -> complete(value)); }
        });
        view = new WebView(context);
        view.getSettings().setJavaScriptEnabled(true);
        view.getSettings().setDomStorageEnabled(true);
        view.getSettings().setAllowFileAccess(false);
        view.getSettings().setAllowContentAccess(false);
        view.getSettings().setJavaScriptCanOpenWindowsAutomatically(false);
        view.getSettings().setSupportMultipleWindows(false);
        view.setWebViewClient(Build.VERSION.SDK_INT >= 26 ? new ModernClient() : new Client());
        result.whenComplete((value, failure) -> { if (result.isCancelled()) close(); });
    }
    private class Client extends WebViewClient {
        @Override public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest request) {
            return !request.isForMainFrame() || !allowed(request.getUrl().toString());
        }
        @SuppressWarnings("deprecation")
        @Override public boolean shouldOverrideUrlLoading(WebView v, String url) { return !allowed(url); }
        @Override public void onReceivedError(WebView v, WebResourceRequest request, WebResourceError error) {
            if (request.isForMainFrame()) server.finish("failed", 0, "the page could not load");
        }
    }
    // Kept in a separate class so API 24/25 do not load API 26 callback types.
    private final class ModernClient extends Client {
        @Override public boolean onRenderProcessGone(WebView view, RenderProcessGoneDetail detail) {
            rendererGone = true;
            server.finish("failed", 0, "web content process terminated");
            return true;
        }
    }
    public WebView view() { requireMain(); return view; }
    public CompletableFuture<Result> run() {
        requireMain();
        if (started || ended) throw new IllegalStateException("bundle runner is single-use");
        started = true;
        try { origin = server.start(); view.loadUrl(origin + "/"); }
        catch (IOException failure) { server.finish("failed", 0, "the loopback listener could not start"); }
        return result;
    }
    /** Thread-safe, bounded queue: 256 messages, each at most 24 MiB UTF-8. */
    public void send(String message) throws IOException { server.send(message); }
    /** Thread-safe, idempotent; release the listener and executing page. */
    @Override public void close() { server.finish("closed", 0, "the host closed the run"); }
    private void complete(Result value) {
        if (ended) return;
        ended = true;
        view.stopLoading(); view.setWebViewClient(new WebViewClient());
        if (!rendererGone) view.loadUrl("about:blank");
        if (view.getParent() instanceof ViewGroup) ((ViewGroup) view.getParent()).removeView(view);
        view.destroy();
        result.complete(value);
    }
    private boolean allowed(String value) {
        try {
            URI uri = URI.create(value);
            return origin != null && "http".equals(uri.getScheme()) && "127.0.0.1".equals(uri.getHost())
                && origin.equals("http://127.0.0.1:" + uri.getPort());
        } catch (IllegalArgumentException failure) { return false; }
    }
    private static void requireMain() {
        if (Looper.myLooper() != Looper.getMainLooper()) throw new IllegalStateException("use the UI thread");
    }
}
