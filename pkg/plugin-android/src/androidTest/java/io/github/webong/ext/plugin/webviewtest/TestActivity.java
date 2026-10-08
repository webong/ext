package io.github.webong.ext.plugin.webviewtest;

import android.app.Activity;
import android.os.Bundle;
import android.util.Log;
import io.github.webong.ext.plugin.webview.WebViewPluginHost;
import io.github.webong.ext.plugin.webview.WebViewPluginHost.Failure;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.TimeZone;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;

/**
 * Runs the ext.conformance/v1 reactor (assets/reactor.wasm) in a real Android WebView,
 * through the real page shim, and reports to logcat under the tag EXTTEST. The same
 * checks as the iOS suite; scripts/plugin-android-webview.sh runs it on an emulator.
 */
public final class TestActivity extends Activity {
    private static final String TAG = "EXTTEST";
    private int passed;
    private int failed;
    private byte[] reactor;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        new Thread(this::runAll, "ext-tests").start();
    }

    private interface Test {
        void run() throws Exception;
    }

    private void runAll() {
        try (InputStream in = getAssets().open("reactor.wasm")) {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buffer = new byte[65536];
            for (int n; (n = in.read(buffer)) > 0; ) {
                out.write(buffer, 0, n);
            }
            reactor = out.toByteArray();
        } catch (Exception e) {
            Log.e(TAG, "cannot read reactor.wasm: " + e);
            Log.i(TAG, "DONE passed=0 failed=1");
            runOnUiThread(this::finish);
            return;
        }
        run("handshake returns the descriptor", this::handshake);
        run("echo carries unicode and large numbers", this::echo);
        run("public error keeps its code", this::publicError);
        run("private error is sanitized", this::privateError);
        run("calls are answered in order", this::ordering);
        run("a guest that honours its deadline answers first", this::honoursDeadline);
        run("a call that overruns destroys the page", this::overrun);
        run("a closed host rejects calls", this::closedHost);
        run("a module that is not a reactor fails to start", this::notAReactor);
        Log.i(TAG, "DONE passed=" + passed + " failed=" + failed);
        runOnUiThread(this::finish);
    }

    private void run(String name, Test test) {
        try {
            test.run();
            passed++;
            Log.i(TAG, "pass: " + name);
        } catch (Throwable t) {
            failed++;
            Log.e(TAG, "FAIL: " + name + ": " + t);
        }
    }

    private static void require(boolean ok, String message) {
        if (!ok) {
            throw new AssertionError(message);
        }
    }

    private WebViewPluginHost host() throws Exception {
        WebViewPluginHost host = new WebViewPluginHost(this, reactor, null, line -> Log.i(TAG, "page: " + line));
        host.start(120_000).get(130, TimeUnit.SECONDS);
        return host;
    }

    private static String text(byte[] data) {
        return new String(data, StandardCharsets.UTF_8);
    }

    private static String request(String operation, String payload, String id, long deadlineMillis) {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        String body = "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"" + id + "\",\"plugin\":{\"id\":\"ctx/conformance\","
                + "\"revision\":\"fixture-1\"},\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},"
                + "\"operation\":\"" + operation + "\",\"deadline\":\"" + format.format(new Date(System.currentTimeMillis() + deadlineMillis)) + "\"";
        return body + (payload == null ? "" : ",\"payload\":" + payload) + "}";
    }

    private static byte[] bytes(String s) {
        return s.getBytes(StandardCharsets.UTF_8);
    }

    private void handshake() throws Exception {
        WebViewPluginHost host = host();
        try {
            String descriptor = text(host.handshake(10_000).get(15, TimeUnit.SECONDS));
            require(descriptor.contains("\"id\":\"ctx/conformance\""), descriptor);
            require(descriptor.contains("\"name\":\"public-error\""), descriptor);
        } finally {
            host.close();
        }
    }

    private void echo() throws Exception {
        WebViewPluginHost host = host();
        try {
            host.handshake(10_000).get(15, TimeUnit.SECONDS);
            String payload = "{\"n\":1.10,\"big\":12345678901234567890,\"s\":\"héllo 日本語 🙂\",\"list\":[1,{\"x\":null}]}";
            String reply = text(host.invoke(bytes(request("echo", payload, "1", 30_000)), 15_000).get(20, TimeUnit.SECONDS));
            require(reply.contains("\"id\":\"1\""), reply);
            require(reply.contains("12345678901234567890"), reply);
            require(reply.contains("héllo 日本語 🙂"), reply);
            require(reply.contains("\"list\":[1,{\"x\":null}]"), reply);
        } finally {
            host.close();
        }
    }

    private void publicError() throws Exception {
        WebViewPluginHost host = host();
        try {
            host.handshake(10_000).get(15, TimeUnit.SECONDS);
            String reply = text(host.invoke(bytes(request("public-error", null, "1", 30_000)), 15_000).get(20, TimeUnit.SECONDS));
            require(reply.contains("\"code\":\"busy\"") && reply.contains("10"), reply);
        } finally {
            host.close();
        }
    }

    private void privateError() throws Exception {
        WebViewPluginHost host = host();
        try {
            host.handshake(10_000).get(15, TimeUnit.SECONDS);
            String reply = text(host.invoke(bytes(request("private-error", null, "1", 30_000)), 15_000).get(20, TimeUnit.SECONDS));
            require(reply.contains("\"error\"") && !reply.toLowerCase(Locale.ROOT).contains("transport"), reply);
        } finally {
            host.close();
        }
    }

    private void ordering() throws Exception {
        WebViewPluginHost host = host();
        try {
            host.handshake(10_000).get(15, TimeUnit.SECONDS);
            for (int i = 0; i < 25; i++) {
                String reply = text(host.invoke(bytes(request("echo", Integer.toString(i), "call-" + i, 30_000)), 15_000).get(20, TimeUnit.SECONDS));
                require(reply.contains("\"id\":\"call-" + i + "\"") && reply.contains("\"payload\":" + i), reply);
            }
        } finally {
            host.close();
        }
    }

    private void honoursDeadline() throws Exception {
        WebViewPluginHost host = host();
        try {
            host.handshake(10_000).get(15, TimeUnit.SECONDS);
            long began = System.nanoTime();
            String reply = text(host.invoke(bytes(request("wait", null, "1", 400)), 10_000).get(20, TimeUnit.SECONDS));
            require((System.nanoTime() - began) / 1_000_000 < 5_000, "took too long");
            require(reply.contains("\"error\""), reply);
        } finally {
            host.close();
        }
    }

    private void overrun() throws Exception {
        WebViewPluginHost host = host();
        host.handshake(10_000).get(15, TimeUnit.SECONDS);
        long began = System.nanoTime();
        try {
            host.invoke(bytes(request("wait", null, "1", 120_000)), 1_000).get(20, TimeUnit.SECONDS);
            throw new AssertionError("the call should have timed out");
        } catch (ExecutionException e) {
            require(e.getCause() instanceof Failure && ((Failure) e.getCause()).kind() == Failure.Kind.TIMEOUT, String.valueOf(e.getCause()));
        }
        require((System.nanoTime() - began) / 1_000_000 < 5_000, "the host must give up at its own deadline");
        try {
            host.invoke(bytes(request("echo", "1", "2", 30_000)), 5_000).get(10, TimeUnit.SECONDS);
            throw new AssertionError("a destroyed host must not answer");
        } catch (ExecutionException e) {
            require(e.getCause() instanceof Failure && ((Failure) e.getCause()).kind() == Failure.Kind.CLOSED, String.valueOf(e.getCause()));
        }
    }

    private void closedHost() throws Exception {
        WebViewPluginHost host = host();
        host.close();
        host.close();
        try {
            host.handshake(5_000).get(10, TimeUnit.SECONDS);
            throw new AssertionError("a closed host must not answer");
        } catch (ExecutionException e) {
            require(e.getCause() instanceof Failure && ((Failure) e.getCause()).kind() == Failure.Kind.CLOSED, String.valueOf(e.getCause()));
        }
    }

    private void notAReactor() throws Exception {
        // A valid module exporting only _start: a command, which the page refuses.
        byte[] command = {0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0, 7, 10, 1, 6, 95, 115, 116, 97, 114, 116, 0, 0, 10, 4, 1, 2, 0, 11};
        WebViewPluginHost host = new WebViewPluginHost(this, command, null, null);
        try {
            host.start(120_000).get(130, TimeUnit.SECONDS);
            throw new AssertionError("a command must not start as a reactor");
        } catch (ExecutionException e) {
            Failure failure = (Failure) e.getCause();
            require(failure.kind() == Failure.Kind.PAGE, failure.kind().toString());
            require(failure.getMessage().contains("_start") || failure.getMessage().contains("export"), failure.getMessage());
        } finally {
            host.close();
        }
    }
}
