package io.github.webong.ext.web.android;

import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.util.*;
import java.util.concurrent.*;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.json.JSONObject;

/** Small HTTP/1.1 loopback origin, one request per connection, bounded readers.
 * The bundle root must remain immutable during the run. */
final class BundleServer {
    interface Events {
        void ready(); void console(String level, String text); void message(String text);
        void finished(BundleRunner.Result result);
    }
    private static final int MAX_FILE = 64 << 20, MAX_MESSAGE = 24 << 20;
    private final File root;
    private final String entry, policy, token;
    private final boolean isolated;
    private final long timeout, startup, heartbeat;
    private final Events events;
    private final ArrayBlockingQueue<String> outbox = new ArrayBlockingQueue<>(256);
    private final Set<Socket> clients = new HashSet<>();
    private final ExecutorService workers = new ThreadPoolExecutor(0, 32, 30, TimeUnit.SECONDS,
        new SynchronousQueue<>(), runnable -> { Thread t = new Thread(runnable, "ext-bundle-http"); t.setDaemon(true); return t; });
    private final ScheduledExecutorService clock = Executors.newSingleThreadScheduledExecutor(runnable -> {
        Thread t = new Thread(runnable, "ext-bundle-clock"); t.setDaemon(true); return t;
    });
    private ServerSocket listener;
    private String host = "", origin = "";
    private boolean ended, seen;
    private long began, last;

    BundleServer(BundleRunner.Options options, Events events) throws IOException {
        root = options.root.getCanonicalFile();
        if (!root.isDirectory()) throw new IOException("bundle root is not a directory");
        entry = options.entry == null || options.entry.isEmpty() ? "index.html" : options.entry;
        policy = options.policy.contentSecurityPolicy(); isolated = options.crossOriginIsolation;
        timeout = options.timeoutMillis; startup = options.startupTimeoutMillis > 0 ? options.startupTimeoutMillis : 30000;
        heartbeat = options.heartbeatTimeoutMillis > 0 ? options.heartbeatTimeoutMillis : 15000;
        this.events = events;
        file(entry);
        byte[] random = new byte[16]; new SecureRandom().nextBytes(random);
        StringBuilder hex = new StringBuilder();
        for (byte value : random) hex.append(String.format(Locale.ROOT, "%02x", value & 255));
        token = hex.toString();
    }
    synchronized String start() throws IOException {
        if (ended || listener != null) throw new IOException("bundle is closed or started");
        listener = new ServerSocket(0, 32, InetAddress.getByName("127.0.0.1"));
        host = "127.0.0.1:" + listener.getLocalPort(); origin = "http://" + host;
        began = now();
        Thread acceptor = new Thread(this::accept, "ext-bundle-listener"); acceptor.setDaemon(true); acceptor.start();
        clock.scheduleAtFixedRate(this::tick, 100, 100, TimeUnit.MILLISECONDS);
        return origin;
    }
    synchronized void send(String text) throws IOException {
        if (text.getBytes(StandardCharsets.UTF_8).length > MAX_MESSAGE) throw new IOException("message too large");
        if (ended) throw new IOException("bundle has ended");
        if (!outbox.offer(text)) throw new IOException("page queue is full");
    }
    synchronized void finish(String status, int code, String reason) {
        if (ended) return;
        ended = true;
        try { if (listener != null) listener.close(); } catch (IOException ignored) { }
        for (Socket client : clients) try { client.close(); } catch (IOException ignored) { }
        clients.clear(); outbox.clear(); workers.shutdownNow(); clock.shutdownNow();
        events.finished(new BundleRunner.Result(status, code, reason, origin.isEmpty() ? "" : origin + "/"));
    }
    private synchronized void touch() {
        if (ended) return;
        boolean first = !seen; seen = true; last = now();
        if (first) events.ready();
    }
    private synchronized void tick() {
        long time = now();
        if (timeout > 0 && time - began >= timeout) finish("timeout", 0, "the run reached its time limit");
        else if (!seen && time - began >= startup) finish("failed", 0, "the page did not load in time");
        else if (seen && time - last >= heartbeat) finish("closed", 0, "the page stopped reporting");
    }
    private static long now() { return TimeUnit.NANOSECONDS.toMillis(System.nanoTime()); }
    private void accept() {
        try {
            while (true) {
                Socket socket = listener.accept(); socket.setSoTimeout(5000);
                synchronized (this) {
                    if (ended) { socket.close(); return; }
                    clients.add(socket);
                }
                try { workers.execute(() -> serve(socket)); }
                catch (RejectedExecutionException full) {
                    synchronized (this) { clients.remove(socket); }
                    socket.close();
                }
            }
        } catch (IOException stopped) {
            synchronized (this) { if (!ended) finish("failed", 0, "the loopback listener failed"); }
        }
    }
    private void serve(Socket socket) {
        ScheduledFuture<?> expiry = null;
        try (Socket client = socket) {
            expiry = clock.schedule(() -> {
                try { socket.close(); } catch (IOException ignored) { }
            }, 25, TimeUnit.SECONDS);
            InputStream in = new BufferedInputStream(client.getInputStream());
            ByteArrayOutputStream header = new ByteArrayOutputStream();
            int state = 0;
            long deadline = now() + 5000;
            while (state != 4) {
                int b = in.read();
                if (b < 0) return;
                header.write(b);
                state = b == (state % 2 == 0 ? '\r' : '\n') ? state + 1 : (b == '\r' ? 1 : 0);
                if (header.size() > 16384 || now() > deadline) { reply(client, 431, "", "text/plain", false); return; }
            }
            String[] lines = new String(header.toByteArray(), StandardCharsets.US_ASCII).split("\r\n");
            String[] first = lines[0].split(" ");
            if (first.length != 3 || !first[1].startsWith("/") || !(first[2].equals("HTTP/1.1") || first[2].equals("HTTP/1.0"))) {
                reply(client, 400, "", "text/plain", false); return;
            }
            Map<String, String> headers = new HashMap<>();
            for (int i = 1; i < lines.length; i++) {
                int colon = lines[i].indexOf(':');
                if (colon < 1 || headers.put(lines[i].substring(0, colon).toLowerCase(Locale.ROOT), lines[i].substring(colon + 1).trim()) != null) {
                    reply(client, 400, "", "text/plain", false); return;
                }
            }
            if (!host.equals(headers.get("host"))) { reply(client, 421, "", "text/plain", false); return; }
            if (headers.containsKey("transfer-encoding")) { reply(client, 400, "", "text/plain", false); return; }
            // URLDecoder is for forms; protect literal '+' before decoding paths.
            String path = URLDecoder.decode(first[1].split("\\?", 2)[0].replace("+", "%2B"), "UTF-8");
            int length = Integer.parseInt(headers.getOrDefault("content-length", "0"));
            int limit = path.equals("/__ext/" + token + "/host") ? 2 * MAX_MESSAGE + 1024 : 256 << 10;
            if (length < 0 || length > limit) { reply(client, 413, "", "text/plain", false); return; }
            byte[] body = new byte[length]; int offset = 0;
            while (offset < length) {
                int count = in.read(body, offset, length - offset);
                if (count < 0 || now() > deadline) return;
                offset += count;
            }
            if (path.startsWith("/__ext/")) control(client, first[0], path, headers, body);
            else resource(client, first[0], path);
        } catch (Exception invalid) {
            // Malformed or abandoned connections are closed; they never finish a run.
        } finally { if (expiry != null) expiry.cancel(false); synchronized (this) { clients.remove(socket); } }
    }
    private void control(Socket client, String method, String path, Map<String, String> headers, byte[] bytes) throws Exception {
        String prefix = "/__ext/" + token + "/";
        if (!path.startsWith(prefix)) { reply(client, 404, "", "text/plain", false); return; }
        String name = path.substring(prefix.length());
        if (name.equals("shim.js") && method.equals("GET")) {
            reply(client, 200, BundleAssets.shim.replace("%TOKEN%", token), "text/javascript; charset=utf-8", false); return;
        }
        if (!origin.equals(headers.getOrDefault("origin", origin)) || !"same-origin".equals(headers.getOrDefault("sec-fetch-site", "same-origin"))) {
            reply(client, 403, "", "text/plain", false); return;
        }
        if (name.equals("next") && method.equals("GET")) {
            touch(); String next = outbox.poll(20, TimeUnit.SECONDS);
            reply(client, next == null ? 204 : 200, next == null ? "" : next, "text/plain; charset=utf-8", false); return;
        }
        if (!method.equals("POST")) { reply(client, 403, "", "text/plain", false); return; }
        JSONObject body;
        try { body = bytes.length == 0 ? new JSONObject() : new JSONObject(new String(bytes, StandardCharsets.UTF_8)); }
        catch (org.json.JSONException invalid) { reply(client, 400, "", "text/plain", false); return; }
        for (String key : new String[]{"level", "text", "data"}) {
            if (!body.isNull(key) && !(body.get(key) instanceof String)) { reply(client, 400, "", "text/plain", false); return; }
        }
        if (!body.isNull("code")) {
            Object code = body.get("code");
            if (!(code instanceof Integer || code instanceof Long) || ((Number) code).longValue() < Integer.MIN_VALUE
                || ((Number) code).longValue() > Integer.MAX_VALUE) { reply(client, 400, "", "text/plain", false); return; }
        }
        switch (name) {
            case "alive": touch(); break;
            case "log": {
                touch(); String level = body.optString("level", "log"), text = body.optString("text", "");
                if (!Arrays.asList("log", "info", "warn", "error", "debug", "exception").contains(level)) level = "log";
                byte[] utf8 = text.getBytes(StandardCharsets.UTF_8);
                if (utf8.length > 65536) text = new String(utf8, 0, 65536, StandardCharsets.UTF_8) + "…";
                events.console(level, text); break;
            }
            case "host": {
                String text = body.optString("data", "");
                if (text.getBytes(StandardCharsets.UTF_8).length > MAX_MESSAGE) { reply(client, 413, "", "text/plain", false); return; }
                touch(); events.message(text); break;
            }
            case "exit": touch(); reply(client, 204, "", "text/plain", false); finish("exited", body.optInt("code", 0), ""); return;
            case "closed": reply(client, 204, "", "text/plain", false); finish("closed", 0, "the page was closed"); return;
            default: reply(client, 404, "", "text/plain", false); return;
        }
        reply(client, 204, "", "text/plain", false);
    }
    private File file(String path) throws IOException {
        if (path.indexOf('\0') >= 0) throw new IOException("invalid path");
        for (String component : path.split("/")) if (component.startsWith(".") && !component.equals(".")) throw new IOException("hidden or parent path");
        while (path.startsWith("/")) path = path.substring(1);
        File file = new File(root, path).getCanonicalFile();
        String prefix = root.getPath().endsWith(File.separator) ? root.getPath() : root.getPath() + File.separator;
        if (!file.getPath().startsWith(prefix) || !file.isFile() || file.length() > MAX_FILE) throw new IOException("outside bundle or not a regular file");
        return file;
    }
    private void resource(Socket client, String method, String path) throws IOException {
        if (!method.equals("GET") && !method.equals("HEAD")) { reply(client, 405, "", "text/plain", false); return; }
        File file;
        try { file = file(path.equals("/") ? entry : path); }
        catch (IOException missing) { reply(client, 404, "", "text/plain", false); return; }
        String type = mime(file.getName()); byte[] data;
        if (path.equals("/") && (file.getName().toLowerCase(Locale.ROOT).endsWith(".js") || file.getName().toLowerCase(Locale.ROOT).endsWith(".mjs"))) {
            StringBuilder escaped = new StringBuilder();
            for (String component : entry.split("/")) {
                if (escaped.length() > 0) escaped.append('/');
                escaped.append(URLEncoder.encode(component, "UTF-8").replace("+", "%20"));
            }
            data = ("<!doctype html><html><head></head><body><script type=\"module\" src=\"/" + escaped + "\"></script></body></html>").getBytes(StandardCharsets.UTF_8);
            type = "text/html; charset=utf-8";
        } else {
            try (InputStream in = new FileInputStream(file); ByteArrayOutputStream out = new ByteArrayOutputStream()) {
                byte[] buffer = new byte[65536]; int n;
                while ((n = in.read(buffer)) != -1) {
                    if (out.size() + n > MAX_FILE) { reply(client, 413, "", "text/plain", false); return; }
                    out.write(buffer, 0, n);
                }
                data = out.toByteArray();
            }
        }
        if (type.startsWith("text/html")) {
            String html = new String(data, StandardCharsets.UTF_8);
            String tag = "<script src=\"/__ext/" + token + "/shim.js\"></script>";
            Matcher head = Pattern.compile("<head(?:\\s[^>]*)?>", Pattern.CASE_INSENSITIVE).matcher(html);
            html = head.find() ? html.substring(0, head.end()) + tag + html.substring(head.end()) : tag + html;
            data = html.getBytes(StandardCharsets.UTF_8);
        }
        reply(client, 200, data, type, method.equals("HEAD"));
    }
    private void reply(Socket socket, int status, String body, String type, boolean head) throws IOException {
        reply(socket, status, body.getBytes(StandardCharsets.UTF_8), type, head);
    }
    private void reply(Socket socket, int status, byte[] body, String type, boolean head) throws IOException {
        String headers = "HTTP/1.1 " + status + " Response\r\nConnection: close\r\nContent-Length: " + body.length
            + "\r\nContent-Type: " + type + "\r\nContent-Security-Policy: " + policy
            + "\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\nReferrer-Policy: no-referrer\r\n";
        if (isolated) headers += "Cross-Origin-Opener-Policy: same-origin\r\nCross-Origin-Embedder-Policy: require-corp\r\nCross-Origin-Resource-Policy: same-origin\r\n";
        OutputStream out = socket.getOutputStream(); out.write((headers + "\r\n").getBytes(StandardCharsets.UTF_8));
        if (!head) out.write(body);
        out.flush();
    }
    private static String mime(String name) {
        String extension = name.substring(name.lastIndexOf('.') + 1).toLowerCase(Locale.ROOT);
        switch (extension) {
            case "html": case "htm": return "text/html; charset=utf-8";
            case "js": case "mjs": return "text/javascript; charset=utf-8";
            case "css": return "text/css"; case "json": return "application/json"; case "wasm": return "application/wasm";
            case "svg": return "image/svg+xml"; case "png": return "image/png"; case "jpg": case "jpeg": return "image/jpeg";
            case "gif": return "image/gif"; case "webp": return "image/webp"; case "woff": return "font/woff";
            case "woff2": return "font/woff2"; case "mp4": return "video/mp4"; case "txt": return "text/plain";
            default: return "application/octet-stream";
        }
    }
}
