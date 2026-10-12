package dev.ext.consumer;

import android.content.Context;
import io.github.webong.ext.plugin.Contract;
import io.github.webong.ext.plugin.Descriptor;
import io.github.webong.ext.plugin.Guest;
import io.github.webong.ext.plugin.Identity;
import io.github.webong.ext.plugin.Operation;
import io.github.webong.ext.plugin.RemoteError;
import io.github.webong.ext.plugin.webview.WebViewPluginHost;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.TimeZone;
import java.util.concurrent.TimeUnit;

/** What an app does with the SDK: author a guest, and host a reactor in a WebView. */
public final class Roundtrips {
    private Roundtrips() {}

    static String request(String plugin, String revision, String contract, String operation, String payload) {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        String deadline = format.format(new Date(System.currentTimeMillis() + 30_000));
        return "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"" + plugin + "\",\"revision\":\""
                + revision + "\"},\"contract\":{\"name\":\"" + contract + "\",\"version\":\"v1\"},\"operation\":\""
                + operation + "\",\"deadline\":\"" + deadline + "\"" + (payload == null ? "" : ",\"payload\":" + payload) + "}";
    }

    /** Authors a guest in the app's own process (the JNI engine) and calls it. */
    public static String guest() throws Exception {
        Descriptor descriptor = new Descriptor(new Identity("app/greeter", "1"),
                List.of(new Contract("app.greeter", "v1", List.of(new Operation("greet"), new Operation("busy")))));
        try (Guest guest = new Guest(descriptor, call -> {
            if (call.operation().equals("busy")) {
                throw new RemoteError("busy", "try later", 10);
            }
            return call.payload();
        })) {
            String ok = new String(guest.invoke(request("app/greeter", "1", "app.greeter", "greet",
                    "{\"n\":1.10,\"s\":\"héllo\"}").getBytes(StandardCharsets.UTF_8)), StandardCharsets.UTF_8);
            String busy = new String(guest.invoke(request("app/greeter", "1", "app.greeter", "busy", null)
                    .getBytes(StandardCharsets.UTF_8)), StandardCharsets.UTF_8);
            return ok + "\n" + busy;
        }
    }

    public static byte[] reactor(Context context) throws Exception {
        try (InputStream in = context.getAssets().open("reactor.wasm")) {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buffer = new byte[65536];
            for (int n; (n = in.read(buffer)) > 0; ) {
                out.write(buffer, 0, n);
            }
            return out.toByteArray();
        }
    }

    /** Hosts the reactor in a WebView and echoes through it. */
    public static String webView(Context context) throws Exception {
        WebViewPluginHost host = new WebViewPluginHost(context, reactor(context), null, line -> {});
        try {
            host.start(120_000).get(130, TimeUnit.SECONDS);
            host.handshake(10_000).get(15, TimeUnit.SECONDS);
            String body = request("ctx/conformance", "fixture-1", "ext.conformance", "echo", "{\"big\":12345678901234567890}");
            return new String(host.invoke(body.getBytes(StandardCharsets.UTF_8), 15_000).get(20, TimeUnit.SECONDS),
                    StandardCharsets.UTF_8);
        } finally {
            host.close();
        }
    }
}
