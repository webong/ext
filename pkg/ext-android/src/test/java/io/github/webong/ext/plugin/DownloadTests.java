package io.github.webong.ext.plugin;

import com.sun.net.httpserver.Headers;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import com.sun.net.httpserver.HttpsConfigurator;
import com.sun.net.httpserver.HttpsServer;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManagerFactory;

/**
 * ArchiveDownload against every case of lib/plugin/testdata/v1/download-cases.json: an HTTPS
 * origin on the loopback interface with a certificate made for this run (by the JDK's keytool) and
 * trusted only through the socket factory the download is given, and a plain HTTP server beside it
 * that must never receive a request. Run from GuestTests.
 */
final class DownloadTests {
    private DownloadTests() {}

    /** A request the origin received: its path and headers. */
    record Seen(String path, Headers headers) {}

    static void cases() throws Exception {
        Map<String, Object> fixture = Json.object(Json.parse(
                Files.readAllBytes(ArchiveTests.fixtures().resolve("download-cases.json"))));
        StoreTests.same((long) ArchiveDownload.MAX_REDIRECTS, ArchiveTests.number(fixture, "maxRedirects"), "maxRedirects");
        Map<String, Object> routes = Json.object(fixture.get("routes"));
        try (StoreTests.Scratch scratch = new StoreTests.Scratch()) {
            SSLContext[] tls = tls(scratch.dir);
            HttpsServer origin = HttpsServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
            origin.setHttpsConfigurator(new HttpsConfigurator(tls[0]));
            HttpServer plain = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
            String originHost = "127.0.0.1:" + origin.getAddress().getPort();
            String plainHost = "127.0.0.1:" + plain.getAddress().getPort();
            List<Seen> seen = new ArrayList<>();
            AtomicInteger plainRequests = new AtomicInteger();
            origin.createContext("/", exchange -> {
                synchronized (seen) {
                    seen.add(new Seen(exchange.getRequestURI().getPath(), exchange.getRequestHeaders()));
                }
                serve(exchange, Json.object(routes.get(exchange.getRequestURI().getPath())), originHost, plainHost);
            });
            plain.createContext("/", exchange -> {
                plainRequests.incrementAndGet();
                serve(exchange, Json.object(routes.get(exchange.getRequestURI().getPath())), originHost, plainHost);
            });
            origin.start();
            plain.start();
            int failures = 0;
            try {
                int n = 0;
                for (Object item : (List<?>) fixture.get("cases")) {
                    Map<String, Object> c = Json.object(item);
                    synchronized (seen) {
                        seen.clear();
                    }
                    try {
                        runCase(c, scratch.path("download-" + n++), tls[1], originHost, plainHost, seen);
                    } catch (AssertionError e) {
                        failures++;
                        System.out.println("  download case " + Json.string(c.get("name")) + ": " + e.getMessage());
                    }
                }
            } finally {
                origin.stop(0);
                plain.stop(0);
            }
            StoreTests.require(plainRequests.get() == 0, "the plain HTTP server received " + plainRequests.get() + " requests");
            if (failures > 0) throw new AssertionError(failures + " download cases failed");
        }
    }

    static void runCase(Map<String, Object> c, Path destination, SSLContext client, String originHost,
            String plainHost, List<Seen> seen) throws Exception {
        String url = Json.string(c.get("url")).replace("{origin}", originHost).replace("{plain}", plainHost);
        Map<String, String> headers = new LinkedHashMap<>();
        Map<String, Object> given = Json.object(c.get("headers"));
        if (given != null) given.forEach((k, v) -> headers.put(k, Json.string(v)));
        boolean exists = Boolean.TRUE.equals(c.get("destinationExists"));
        byte[] before = "already here".getBytes(StandardCharsets.UTF_8);
        if (exists) Files.write(destination, before);
        ArchiveDownload.Options options = new ArchiveDownload.Options(ArchiveTests.number(c, "maxBytes"), headers,
                10_000, client.getSocketFactory());
        String expect = Json.string(c.get("expect"));
        String got;
        try {
            ArchiveDownload.download(url, destination.toFile(), options);
            got = "ok";
        } catch (GuestException e) {
            got = e.status() == PackageStore.INVALID ? "invalid" : "status " + e.status();
        } catch (IOException e) {
            got = "failed";
        }
        StoreTests.same(expect, got, "outcome");
        if (expect.equals("ok")) {
            StoreTests.same(Json.string(c.get("body")), new String(Files.readAllBytes(destination), StandardCharsets.UTF_8), "body");
        } else if (exists) {
            StoreTests.same(before, Files.readAllBytes(destination), "the existing file");
        } else {
            StoreTests.require(!Files.exists(destination), "a failed download left " + destination);
        }
        Object absent = c.get("absentAtLanding");
        if (absent instanceof List<?> names) {
            Seen landing;
            synchronized (seen) {
                StoreTests.require(!seen.isEmpty(), "the origin received nothing");
                landing = seen.get(seen.size() - 1);
            }
            for (Object name : names) {
                StoreTests.require(!landing.headers().containsKey(Json.string(name)),
                        Json.string(name) + " reached " + landing.path());
            }
        }
    }

    static void serve(HttpExchange exchange, Map<String, Object> route, String originHost, String plainHost)
            throws IOException {
        try (exchange) {
            exchange.getRequestBody().readAllBytes();
            if (route == null) {
                exchange.sendResponseHeaders(404, -1);
                return;
            }
            String redirect = Json.string(route.get("redirect"));
            if (redirect != null) {
                exchange.getResponseHeaders().set("Location", redirect.replace("{origin}", originHost).replace("{plain}", plainHost));
                exchange.sendResponseHeaders(302, -1);
                return;
            }
            byte[] body;
            long size = ArchiveTests.number(route, "bodyBytes");
            if (size > 0) {
                body = "x".repeat((int) size).getBytes(StandardCharsets.US_ASCII);
            } else {
                body = Json.string(route.get("body")).getBytes(StandardCharsets.UTF_8);
            }
            long status = ArchiveTests.number(route, "status");
            boolean chunked = Boolean.TRUE.equals(route.get("chunked"));
            exchange.sendResponseHeaders(status == 0 ? 200 : (int) status, chunked ? 0 : body.length);
            try (OutputStream out = exchange.getResponseBody()) {
                out.write(body);
            } catch (IOException e) {
                // the client stopped reading at its limit
            }
        }
    }

    /** The origin's TLS context and the client's, which trusts only the origin's certificate. */
    static SSLContext[] tls(Path dir) throws Exception {
        char[] password = "ext-test".toCharArray();
        File keystore = dir.resolve("origin.p12").toFile();
        Path keytool = Path.of(System.getProperty("java.home"), "bin", "keytool");
        Process process = new ProcessBuilder(keytool.toString(), "-genkeypair", "-alias", "origin", "-keyalg", "EC",
                "-groupname", "secp256r1", "-dname", "CN=localhost", "-ext", "san=ip:127.0.0.1,dns:localhost",
                "-validity", "1", "-storetype", "PKCS12", "-keystore", keystore.toString(),
                "-storepass", new String(password), "-keypass", new String(password))
                .redirectErrorStream(true).start();
        byte[] output = process.getInputStream().readAllBytes();
        if (process.waitFor() != 0) {
            throw new AssertionError("keytool failed: " + new String(output, StandardCharsets.UTF_8));
        }
        KeyStore keys = KeyStore.getInstance("PKCS12");
        try (InputStream in = Files.newInputStream(keystore.toPath())) {
            keys.load(in, password);
        }
        KeyManagerFactory kmf = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        kmf.init(keys, password);
        SSLContext server = SSLContext.getInstance("TLS");
        server.init(kmf.getKeyManagers(), null, null);

        KeyStore trusted = KeyStore.getInstance("PKCS12");
        trusted.load(null, null);
        trusted.setCertificateEntry("origin", keys.getCertificate("origin"));
        TrustManagerFactory tmf = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        tmf.init(trusted);
        SSLContext client = SSLContext.getInstance("TLS");
        client.init(null, tmf.getTrustManagers(), null);
        return new SSLContext[] {server, client};
    }
}
