package io.github.webong.ext.plugin.webview;

import android.content.Context;
import android.os.Handler;
import android.os.Looper;
import android.util.Base64;
import android.webkit.JavascriptInterface;
import android.webkit.WebSettings;
import android.webkit.WebView;
import java.nio.charset.StandardCharsets;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;
import java.util.TimeZone;
import java.util.concurrent.CompletableFuture;
import org.json.JSONException;
import org.json.JSONObject;

/**
 * Hosts an ext.plugin/v1 WebAssembly reactor (docs/plugin-reactor-abi.md) in a hidden
 * {@link WebView} and exchanges frames with it. The WebView is the platform's own web
 * engine, so this is the {@code restricted} route of docs/adr-mobile-wasm.md: the
 * guest has no network, files or process, but runs under the browser's rules and not
 * a capability sandbox.
 *
 * <p>The page speaks through {@code window.ext.host}, the transport-neutral page API
 * of res/web/bundle. The host registers the {@code ExtHost} JavaScript interface that
 * the page shim's native transport posts to, and delivers frames with
 * {@code window.ext.host._deliver}.
 *
 * <p>A running WebAssembly call cannot be interrupted, so a call that overruns its
 * timeout destroys the WebView and closes the host. Use one host per plugin. Methods
 * may be called from any thread; futures complete on the main thread, so never block
 * the main thread waiting for one.
 */
public final class WebViewPluginHost {
    /** Why a call did not return a frame. */
    public static final class Failure extends Exception {
        private static final long serialVersionUID = 1L;

        /** What went wrong. */
        public enum Kind { NOT_STARTED, CLOSED, TIMEOUT, PAGE, STATUS }

        private final Kind kind;
        private final int status;

        Failure(Kind kind, String message, int status) {
            super(message);
            this.kind = kind;
            this.status = status;
        }

        public Kind kind() {
            return kind;
        }

        /** The ext_plugin_call status when {@link #kind()} is STATUS (1 invalid, 2 closed, 3 failed). */
        public int status() {
            return status;
        }
    }

    /** Receives page and guest diagnostics. */
    public interface Log {
        void line(String text);
    }

    private final Handler main = new Handler(Looper.getMainLooper());
    private final Context context;
    private final byte[] module;
    private final String shim;
    private final Log log;

    // Main-thread state.
    private WebView webView;
    private boolean started;
    private boolean closed;
    private CompletableFuture<Void> ready;
    private final Map<Integer, CompletableFuture<byte[]>> pending = new HashMap<>();
    private int nextSequence = 1;

    /**
     * @param context an Activity or Application context
     * @param module the reactor module's bytes
     * @param shim the page shim, res/web/bundle/shim.js unchanged; null uses the copy
     *     this library embeds (scripts/plugin-mobile-assets.sh)
     * @param log may be null
     */
    public WebViewPluginHost(Context context, byte[] module, String shim, Log log) {
        this.context = context;
        this.module = module.clone();
        this.shim = shim != null ? shim : GeneratedAssets.shim;
        this.log = log;
    }

    /** Creates the WebView, instantiates the module and completes when the page is ready. */
    public CompletableFuture<Void> start(long timeoutMillis) {
        CompletableFuture<Void> result = new CompletableFuture<>();
        main.post(() -> {
            if (started || closed) {
                result.completeExceptionally(new Failure(Failure.Kind.CLOSED, "host already started or closed", 0));
                return;
            }
            started = true;
            ready = result;
            WebView view = new WebView(context);
            WebSettings settings = view.getSettings();
            settings.setJavaScriptEnabled(true);
            settings.setAllowFileAccess(false);
            settings.setAllowContentAccess(false);
            settings.setBlockNetworkLoads(true);
            view.addJavascriptInterface(new Bridge(), "ExtHost");
            webView = view;
            view.loadDataWithBaseURL("https://ext.invalid/", page(), "text/html", "utf-8", null);
            main.postDelayed(() -> {
                if (ready == result && !result.isDone()) {
                    result.completeExceptionally(new Failure(Failure.Kind.TIMEOUT, "the page did not become ready", 0));
                    close();
                }
            }, timeoutMillis);
        });
        return result;
    }

    /** Sends the handshake and returns the descriptor JSON. */
    public CompletableFuture<byte[]> handshake(long timeoutMillis) {
        String hello = "{\"deadline\":\"" + timestamp(System.currentTimeMillis() + timeoutMillis) + "\"}";
        return exchange(1, hello.getBytes(StandardCharsets.UTF_8), timeoutMillis);
    }

    /** Sends one complete ext.plugin/v1 request and returns the complete response. */
    public CompletableFuture<byte[]> invoke(byte[] request, long timeoutMillis) {
        return exchange(2, request, timeoutMillis);
    }

    /** Destroys the WebView. Pending calls fail with CLOSED. Idempotent; any thread. */
    public void close() {
        main.post(() -> {
            if (closed) {
                return;
            }
            closed = true;
            if (webView != null) {
                webView.removeJavascriptInterface("ExtHost");
                webView.loadUrl("about:blank");
                webView.destroy();
                webView = null;
            }
            Failure gone = new Failure(Failure.Kind.CLOSED, "host closed", 0);
            if (ready != null && !ready.isDone()) {
                ready.completeExceptionally(gone);
            }
            Map<Integer, CompletableFuture<byte[]>> waiting = new HashMap<>(pending);
            pending.clear();
            for (CompletableFuture<byte[]> future : waiting.values()) {
                future.completeExceptionally(gone);
            }
        });
    }

    private CompletableFuture<byte[]> exchange(int operation, byte[] frame, long timeoutMillis) {
        CompletableFuture<byte[]> result = new CompletableFuture<>();
        main.post(() -> {
            if (!started) {
                result.completeExceptionally(new Failure(Failure.Kind.NOT_STARTED, "start() was not called", 0));
                return;
            }
            if (closed || webView == null) {
                result.completeExceptionally(new Failure(Failure.Kind.CLOSED, "host closed", 0));
                return;
            }
            if (timeoutMillis <= 0) {
                result.completeExceptionally(new Failure(Failure.Kind.TIMEOUT, "deadline already passed", 0));
                close();
                return;
            }
            int sequence = nextSequence++;
            pending.put(sequence, result);
            try {
                JSONObject message = new JSONObject();
                message.put("seq", sequence);
                message.put("op", operation);
                message.put("frame", new String(frame, StandardCharsets.UTF_8));
                webView.evaluateJavascript("window.ext.host._deliver(" + JSONObject.quote(message.toString()) + ")", null);
            } catch (JSONException e) {
                pending.remove(sequence);
                result.completeExceptionally(new Failure(Failure.Kind.PAGE, e.toString(), 0));
                return;
            }
            main.postDelayed(() -> {
                // The call overran its deadline. It cannot be interrupted, so the page is
                // destroyed, which also fails every other pending call.
                if (pending.remove(sequence) != null) {
                    result.completeExceptionally(new Failure(Failure.Kind.TIMEOUT, "call overran its deadline", 0));
                    close();
                }
            }, timeoutMillis);
        });
        return result;
    }

    private String page() {
        return "<!doctype html><html><head><meta charset=\"utf-8\"><script>" + shim + "</script></head><body><script>\n"
                + "const MODULE_BASE64 = \"" + Base64.encodeToString(module, Base64.NO_WRAP) + "\";\n"
                + GeneratedAssets.reactorPage + "\n</script></body></html>";
    }

    private static String timestamp(long millis) {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date(millis));
    }

    // The page posts here from a WebView-owned thread; hop to the main thread.
    private final class Bridge {
        @JavascriptInterface
        public void postMessage(String json) {
            main.post(() -> receive(json));
        }
    }

    private void receive(String json) {
        try {
            JSONObject event = new JSONObject(json);
            switch (event.optString("kind")) {
                case "host":
                    handle(new JSONObject(event.getString("data")));
                    break;
                case "log":
                    if (log != null) {
                        log.line(event.optString("text"));
                    }
                    break;
                default:
                    break; // alive, closed and exit need no action in an embedded host
            }
        } catch (JSONException ignored) {
            // A malformed event is not ours; ignore it.
        }
    }

    private void handle(JSONObject message) {
        String kind = message.optString("kind");
        if (kind.equals("ready")) {
            if (ready != null) {
                ready.complete(null);
            }
        } else if (kind.equals("failed")) {
            Failure failure = new Failure(Failure.Kind.PAGE, message.optString("reason", "the page failed"), 0);
            if (ready != null && !ready.isDone()) {
                ready.completeExceptionally(failure);
            }
            close();
        } else if (message.has("seq")) {
            CompletableFuture<byte[]> future = pending.remove(message.optInt("seq"));
            if (future == null) {
                return;
            }
            int status = message.optInt("status", 3);
            if (status != 0) {
                future.completeExceptionally(new Failure(Failure.Kind.STATUS, "ext_plugin_call status " + status, status));
            } else {
                future.complete(message.optString("frame", "").getBytes(StandardCharsets.UTF_8));
            }
        }
    }
}
