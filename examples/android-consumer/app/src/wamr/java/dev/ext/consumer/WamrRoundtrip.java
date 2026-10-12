package dev.ext.consumer;

import android.content.Context;
import io.github.webong.ext.plugin.WamrPluginHost;
import java.nio.charset.StandardCharsets;

/** Hosts the reactor in-process on WAMR and echoes through it. */
public final class WamrRoundtrip {
    private WamrRoundtrip() {}

    public static String echo(Context context) throws Exception {
        byte[] module = Roundtrips.reactor(context);
        byte[] descriptor = WamrPluginHost.descriptorOf(module, 30_000, null);
        try (WamrPluginHost host = new WamrPluginHost(module, descriptor)) {
            host.start(30_000);
            String body = Roundtrips.request("ctx/conformance", "fixture-1", "ext.conformance", "echo", "{\"big\":12345678901234567890}");
            return new String(host.invoke(body.getBytes(StandardCharsets.UTF_8), 15_000), StandardCharsets.UTF_8);
        }
    }
}
