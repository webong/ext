package io.github.webong.ext.web.android;

import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.*;
import java.util.function.BiConsumer;

/** Device-side protocol tests that do not need a WebView or AndroidX. */
public final class BundleServerChecks {
    private BundleServerChecks() {}
    public static void run(File root, BiConsumer<Boolean, String> check) throws Exception {
        BundleRunner.Options options = new BundleRunner.Options(root); options.entry = "main.mjs";
        options.crossOriginIsolation = true;
        CompletableFuture<BundleRunner.Result> result = new CompletableFuture<>();
        BundleServer server = new BundleServer(options, events(result));
        try {
            String url = server.start();
            HttpURLConnection html = request(url + "/", "GET", null);
            String content = read(html);
            check.accept(html.getResponseCode() == 200 && "require-corp".equals(html.getHeaderField("Cross-Origin-Embedder-Policy")), "bundle isolation headers");
            check.accept(options.policy.contentSecurityPolicy().equals(html.getHeaderField("Content-Security-Policy")), "bundle CSP header");
            String prefix = content.substring(content.indexOf("/__ext/"), content.indexOf("/shim.js") + 1);
            for (String path : new String[]{"/.secret", "/%2e%2e/outside", "/__ext/bad/alive"}) {
                HttpURLConnection missing = request(url + path, "GET", null);
                check.accept(missing.getResponseCode() == 404, "bundle confined path " + path); missing.disconnect();
            }
            File outside = new File(root.getParentFile(), "outside-" + java.util.UUID.randomUUID());
            try (FileOutputStream out = new FileOutputStream(outside)) { out.write(42); }
            File link = new File(root, "escape.txt");
            try {
                android.system.Os.symlink(outside.getPath(), link.getPath());
                HttpURLConnection escape = request(url + "/escape.txt", "GET", null);
                check.accept(escape.getResponseCode() == 404, "bundle denies escaping symlink"); escape.disconnect();
            } finally { link.delete(); outside.delete(); }
            HttpURLConnection denied = request(url + prefix + "alive", "POST", "https://other.invalid");
            check.accept(denied.getResponseCode() == 403, "bundle denies cross-origin control"); denied.disconnect();
            try (Socket socket = new Socket("127.0.0.1", URI.create(url).getPort())) {
                socket.setSoTimeout(5000);
                socket.getOutputStream().write("GET / HTTP/1.1\r\nHost: rebound.invalid\r\n\r\n".getBytes(StandardCharsets.US_ASCII));
                check.accept(new BufferedReader(new InputStreamReader(socket.getInputStream(), StandardCharsets.US_ASCII)).readLine().contains("421"), "bundle rejects rebound host");
            }
            HttpURLConnection malformed = (HttpURLConnection) new URL(url + prefix + "exit").openConnection();
            malformed.setReadTimeout(3000); malformed.setRequestMethod("POST"); malformed.setDoOutput(true);
            malformed.getOutputStream().write("{\"code\":\"wrong-type\"}".getBytes(StandardCharsets.UTF_8));
            check.accept(malformed.getResponseCode() == 400, "bundle rejects malformed event fields"); malformed.disconnect();
            for (int i = 0; i < 256; i++) server.send("queued");
            try { server.send("overflow"); check.accept(false, "bundle queue bounded"); }
            catch (IOException expected) { check.accept(true, "bundle queue bounded"); }
            html.disconnect();
        } finally { server.finish("closed", 0, "test complete"); }
        check.accept(result.get(3, TimeUnit.SECONDS).status.equals("closed"), "bundle server closes");
        try { server.send("late"); check.accept(false, "bundle server rejects late send"); }
        catch (IOException expected) { check.accept(true, "bundle server rejects late send"); }
        options.startupTimeoutMillis = 200;
        CompletableFuture<BundleRunner.Result> startup = new CompletableFuture<>();
        BundleServer unvisited = new BundleServer(options, events(startup));
        try {
            unvisited.start();
            check.accept(startup.get(3, TimeUnit.SECONDS).status.equals("failed"), "bundle startup timeout");
        } finally { unvisited.finish("closed", 0, "cleanup"); }
        options.startupTimeoutMillis = 3000; options.heartbeatTimeoutMillis = 200;
        CompletableFuture<BundleRunner.Result> silent = new CompletableFuture<>();
        BundleServer heartbeat = new BundleServer(options, events(silent));
        try {
            String url = heartbeat.start();
            HttpURLConnection html = request(url + "/", "GET", null); String content = read(html); html.disconnect();
            String prefix = content.substring(content.indexOf("/__ext/"), content.indexOf("/shim.js") + 1);
            HttpURLConnection alive = request(url + prefix + "alive", "POST", null);
            check.accept(alive.getResponseCode() == 204, "bundle accepts same-origin report"); alive.disconnect();
            check.accept(silent.get(3, TimeUnit.SECONDS).status.equals("closed"), "bundle heartbeat timeout");
        } finally { heartbeat.finish("closed", 0, "cleanup"); }
    }
    private static BundleServer.Events events(CompletableFuture<BundleRunner.Result> future) {
        return new BundleServer.Events() {
            public void ready() {} public void console(String level, String text) {} public void message(String text) {}
            public void finished(BundleRunner.Result result) { future.complete(result); }
        };
    }
    private static HttpURLConnection request(String url, String method, String origin) throws Exception {
        HttpURLConnection connection = (HttpURLConnection) new URL(url).openConnection();
        connection.setConnectTimeout(3000); connection.setReadTimeout(3000); connection.setRequestMethod(method);
        if (origin != null) connection.setRequestProperty("Origin", origin);
        if (method.equals("POST")) { connection.setDoOutput(true); connection.getOutputStream().write("{}".getBytes(StandardCharsets.UTF_8)); }
        return connection;
    }
    private static String read(HttpURLConnection connection) throws Exception {
        try (InputStream in = connection.getInputStream(); ByteArrayOutputStream out = new ByteArrayOutputStream()) {
            byte[] bytes = new byte[4096]; int n;
            while ((n = in.read(bytes)) >= 0) out.write(bytes, 0, n);
            return new String(out.toByteArray(), StandardCharsets.UTF_8);
        }
    }
}
