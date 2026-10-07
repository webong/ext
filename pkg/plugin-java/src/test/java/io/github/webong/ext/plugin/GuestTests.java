package io.github.webong.ext.plugin;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;

/** A plain runner (no test framework is assumed): run with `java -ea`. */
public final class GuestTests {
    private static int failures;

    public static void main(String[] args) throws Exception {
        run("descriptor is served by the engine", GuestTests::descriptor);
        run("echo keeps number text", GuestTests::echoKeepsNumbers);
        run("absent payload becomes null", GuestTests::absentPayload);
        run("public error keeps code and retry hint", GuestTests::publicError);
        run("private error is sanitized", GuestTests::privateError);
        run("operation outside the descriptor is rejected", GuestTests::outsideDescriptor);
        run("malformed request is a transport failure", GuestTests::malformed);
        run("expired deadline does not run the handler", GuestTests::expired);
        run("cooperative wait stops at the deadline", GuestTests::waitStops);
        run("concurrent invocations", GuestTests::concurrent);
        run("closed guest rejects calls and close is idempotent", GuestTests::closed);
        run("json line serves hello then requests", GuestTests::jsonLine);
        run("json line rejects malformed and bad hello frames", GuestTests::jsonLineRejects);
        System.out.println(failures == 0 ? "all Java plugin tests passed" : failures + " Java plugin tests FAILED");
        System.exit(failures == 0 ? 0 : 1);
    }

    interface Check {
        void run() throws Exception;
    }

    static void run(String name, Check check) {
        try {
            check.run();
            System.out.println("ok   " + name);
        } catch (Throwable t) {
            failures++;
            System.out.println("FAIL " + name + ": " + t);
        }
    }

    static void require(boolean ok, String message) {
        if (!ok) {
            throw new AssertionError(message);
        }
    }

    static final Descriptor DESCRIPTOR = new Descriptor(
            new Identity("java/test", "r1"),
            List.of(new Contract("test", "v1", List.of(
                    new Operation("echo"), new Operation("boom"), new Operation("busy"), new Operation("wait")))));

    static Guest guest() throws GuestException {
        return new Guest(DESCRIPTOR, call -> {
            switch (call.operation()) {
                case "boom":
                    throw new IllegalStateException("secret");
                case "busy":
                    throw new RemoteError("busy", "try later", 10);
                case "wait":
                    while (true) {
                        call.checkDeadline();
                        Thread.sleep(1);
                    }
                default:
                    return call.payload();
            }
        });
    }

    static byte[] request(String operation, String payload, String deadline) {
        String body = "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"java/test\",\"revision\":\"r1\"},"
                + "\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"" + operation
                + "\",\"deadline\":\"" + deadline + "\"" + (payload == null ? "" : ",\"payload\":" + payload) + "}";
        return body.getBytes(StandardCharsets.UTF_8);
    }

    static byte[] request(String operation, String payload) {
        return request(operation, payload, "2099-01-01T00:00:00Z");
    }

    static String text(byte[] data) {
        return new String(data, StandardCharsets.UTF_8);
    }

    static void descriptor() throws Exception {
        try (Guest g = guest()) {
            String json = text(g.descriptorJson());
            require(json.contains("\"apiVersion\":\"ext.plugin/v1\""), json);
            require(json.contains("\"id\":\"java/test\"") && json.contains("\"name\":\"echo\""), json);
        }
    }

    static void echoKeepsNumbers() throws Exception {
        String payload = "{\"n\":1.10,\"big\":12345678901234567890,\"s\":\"é\\n\",\"list\":[1,{\"x\":null}]}";
        try (Guest g = guest()) {
            String response = text(g.invoke(request("echo", payload)));
            require(response.contains("\"payload\":" + payload), response);
            require(response.contains("\"id\":\"1\""), response);
        }
    }

    static void absentPayload() throws Exception {
        try (Guest g = guest()) {
            require(text(g.invoke(request("echo", null))).contains("\"payload\":null"), "absent payload");
        }
    }

    static void publicError() throws Exception {
        try (Guest g = guest()) {
            String response = text(g.invoke(request("busy", null)));
            require(response.contains("\"code\":\"busy\"") && response.contains("\"retryAfterMilliseconds\":10"), response);
            require(!response.contains("payload"), response);
        }
    }

    static void privateError() throws Exception {
        try (Guest g = guest()) {
            String response = text(g.invoke(request("boom", null)));
            require(response.contains("\"code\":\"operation_failed\""), response);
            require(!response.contains("secret"), response);
        }
    }

    static void outsideDescriptor() throws Exception {
        try (Guest g = guest()) {
            require(text(g.invoke(request("missing", null))).contains("invalid_request"), "rejected");
        }
    }

    static void malformed() throws Exception {
        try (Guest g = guest()) {
            try {
                g.invoke("{".getBytes(StandardCharsets.UTF_8));
                throw new AssertionError("accepted");
            } catch (GuestException expected) {
                require(expected.status() != 0, "status");
            }
        }
    }

    static void expired() throws Exception {
        try (Guest g = guest()) {
            String response = text(g.invoke(request("echo", "1", "2001-01-01T00:00:00Z")));
            require(response.contains("operation_failed"), response);
        }
    }

    static void waitStops() throws Exception {
        try (Guest g = guest()) {
            long started = System.nanoTime();
            String response = text(g.invoke(request("wait", null), 80));
            require((System.nanoTime() - started) / 1_000_000 < 2000, "took too long");
            require(response.contains("operation_failed"), response);
        }
    }

    static void concurrent() throws Exception {
        try (Guest g = guest()) {
            AtomicInteger bad = new AtomicInteger();
            Thread[] threads = new Thread[8];
            for (int t = 0; t < threads.length; t++) {
                final int base = t * 100;
                threads[t] = new Thread(() -> {
                    for (int i = 0; i < 100; i++) {
                        try {
                            String response = text(g.invoke(request("echo", Integer.toString(base + i))));
                            if (!response.contains("\"payload\":" + (base + i))) {
                                bad.incrementAndGet();
                            }
                        } catch (Exception e) {
                            bad.incrementAndGet();
                        }
                    }
                });
                threads[t].start();
            }
            for (Thread thread : threads) {
                thread.join();
            }
            require(bad.get() == 0, bad.get() + " bad responses");
        }
    }

    static void closed() throws Exception {
        Guest g = guest();
        g.close();
        g.close();
        try {
            g.invoke(request("echo", "1"));
            throw new AssertionError("call after close succeeded");
        } catch (GuestException expected) {
            require(expected.status() == 5, "closed status " + expected.status());
        }
    }

    static final String HELLO = "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\","
            + "\"deadline\":\"2099-01-01T00:00:00Z\"}";

    static void jsonLine() throws Exception {
        String input = HELLO + "\n" + text(request("echo", "{\"v\":7}")) + "\n";
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        try (Guest g = guest()) {
            JsonLine.serve(g, new ByteArrayInputStream(input.getBytes(StandardCharsets.UTF_8)), out);
        }
        String[] lines = text(out.toByteArray()).split("\n");
        require(lines.length == 2, "lines: " + lines.length);
        require(lines[0].startsWith("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"payload\":{")
                && lines[0].contains("\"id\":\"java/test\""), lines[0]);
        require(lines[1].contains("\"payload\":{\"v\":7}"), lines[1]);
    }

    static void jsonLineRejects() throws Exception {
        List<String> bad = List.of(
                "{\"x\":1,\"x\":2}", "{} {}", "{\"x\":}", "[1,]", "{\"x\":1,\"\\u0078\":2}",
                HELLO.replace("plugin.hello", "plugin.other"),
                HELLO.replace("\"hello\"", "\"other\""),
                HELLO.replace("2099-01-01T00:00:00Z", "2001-01-01T00:00:00Z"),
                HELLO.replace("}", ",\"extra\":1}"));
        for (String frame : bad) {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            try (Guest g = guest()) {
                JsonLine.serve(g, new ByteArrayInputStream((frame + "\n").getBytes(StandardCharsets.UTF_8)), out);
                throw new AssertionError("accepted " + frame);
            } catch (IOException expected) {
                require(out.size() == 0, "wrote output for " + frame);
            }
        }
    }
}
