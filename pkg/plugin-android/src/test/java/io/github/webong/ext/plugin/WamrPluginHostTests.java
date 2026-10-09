package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.time.Duration;
import java.time.Instant;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;

/**
 * Runs the shared ext.conformance/v1 reactor (the Rust SDK's shared guest built for
 * wasm32-wasip1) on the in-process WAMR interpreter. Run with {@code java -ea} and the
 * extwamrjni library on java.library.path; pass the module as the argument or set
 * EXT_RUST_REACTOR.
 */
public final class WamrPluginHostTests {
    private static int failures;
    private static byte[] module;
    private static Consumer<String> log;

    public static void main(String[] args) throws Exception {
        String path = args.length > 0 ? args[0] : System.getenv("EXT_RUST_REACTOR");
        if (path == null || path.isEmpty()) {
            System.out.println("skipped: pass the wasm32-wasip1 build of the Rust shared example");
            return;
        }
        System.exit(runAll(Files.readAllBytes(Paths.get(path)), System.out::println) == 0 ? 0 : 1);
    }

    /** Runs every check against the module, reporting each line to {@code log}; returns the failures. */
    public static synchronized int runAll(byte[] reactor, Consumer<String> output) {
        module = reactor;
        log = output;
        failures = 0;
        run("inspection returns the module's own descriptor", WamrPluginHostTests::inspection);
        run("echo carries unicode and large numbers", WamrPluginHostTests::echo);
        run("public error keeps its code and private error is sanitized", WamrPluginHostTests::errors);
        run("calls are answered in order", WamrPluginHostTests::ordering);
        run("a mismatched descriptor is refused at start", WamrPluginHostTests::mismatch);
        run("an authorization denial leaves the host usable", WamrPluginHostTests::denial);
        run("the host stops a guest at its request deadline", WamrPluginHostTests::deadline);
        run("a call that overruns its timeout ends the instance", WamrPluginHostTests::timeout);
        run("close from another thread interrupts a running call", WamrPluginHostTests::closeInterrupts);
        run("a module that is not a reactor is refused", WamrPluginHostTests::notReactor);
        run("an instruction budget stops a runaway call", WamrPluginHostTests::budget);
        run("a closed host rejects calls", WamrPluginHostTests::closedHost);
        log.accept(failures == 0 ? "all Java WAMR host tests passed" : failures + " Java WAMR host tests FAILED");
        return failures;
    }

    interface Check {
        void run() throws Exception;
    }

    static void run(String name, Check check) {
        try {
            check.run();
            log.accept("ok   " + name);
        } catch (Throwable t) {
            failures++;
            log.accept("FAIL " + name + ": " + t);
        }
    }

    static void require(boolean ok, String message) {
        if (!ok) {
            throw new AssertionError(message);
        }
    }

    static void requireStatus(Check call, int status) throws Exception {
        try {
            call.run();
        } catch (WamrException e) {
            require(e.status() == status, "expected ext_status " + status + ", got " + e);
            return;
        }
        throw new AssertionError("expected ext_status " + status + ", but the call succeeded");
    }

    static void requireFails(Check call) throws Exception {
        try {
            call.run();
        } catch (WamrException e) {
            return;
        }
        throw new AssertionError("expected the call to fail");
    }

    static String text(byte[] data) {
        return new String(data, StandardCharsets.UTF_8);
    }

    static WamrPluginHost started(WamrPluginHost.Options options, java.util.function.Predicate<byte[]> authorize)
            throws WamrException {
        byte[] descriptor = WamrPluginHost.descriptorOf(module, 30_000, null);
        WamrPluginHost host = new WamrPluginHost(module, descriptor, options, null, authorize, null);
        host.start(30_000);
        return host;
    }

    static WamrPluginHost started() throws WamrException {
        return started(null, null);
    }

    static byte[] request(String operation, String payload, String id, Instant deadline) {
        String body = "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"" + id + "\",\"plugin\":{\"id\":\"ctx/conformance\","
                + "\"revision\":\"fixture-1\"},\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},"
                + "\"operation\":\"" + operation + "\",\"deadline\":\"" + deadline + "\""
                + (payload == null ? "" : ",\"payload\":" + payload) + "}";
        return body.getBytes(StandardCharsets.UTF_8);
    }

    static byte[] request(String operation, String payload) {
        return request(operation, payload, "1", Instant.now().plus(Duration.ofSeconds(30)));
    }

    static void inspection() throws Exception {
        String descriptor = text(WamrPluginHost.descriptorOf(module, 30_000, null));
        require(descriptor.contains("\"id\":\"ctx/conformance\""), descriptor);
        require(descriptor.contains("\"name\":\"public-error\""), descriptor);
    }

    static void echo() throws Exception {
        try (WamrPluginHost host = started()) {
            String payload = "{\"n\":1.10,\"big\":12345678901234567890,\"s\":\"héllo 日本語 🙂\",\"list\":[1,{\"x\":null}]}";
            String reply = text(host.invoke(request("echo", payload), 30_000));
            require(reply.contains("\"id\":\"1\""), reply);
            require(reply.contains("12345678901234567890"), reply);
            require(reply.contains("héllo 日本語 🙂"), reply);
            require(reply.contains("\"list\":[1,{\"x\":null}]"), reply);
        }
    }

    static void errors() throws Exception {
        try (WamrPluginHost host = started()) {
            String busy = text(host.invoke(request("public-error", null), 30_000));
            require(busy.contains("\"code\":\"busy\""), busy);
            String hidden = text(host.invoke(request("private-error", null), 30_000));
            require(hidden.contains("\"error\"") && !hidden.toLowerCase().contains("transport"), hidden);
        }
    }

    static void ordering() throws Exception {
        try (WamrPluginHost host = started()) {
            for (int i = 0; i < 50; i++) {
                byte[] call = request("echo", Integer.toString(i), "call-" + i, Instant.now().plusSeconds(30));
                String reply = text(host.invoke(call, 30_000));
                require(reply.contains("\"id\":\"call-" + i + "\"") && reply.contains("\"payload\":" + i), reply);
            }
        }
    }

    static void mismatch() throws Exception {
        String other = text(WamrPluginHost.descriptorOf(module, 30_000, null)).replace("fixture-1", "fixture-2");
        try (WamrPluginHost host = new WamrPluginHost(module, other.getBytes(StandardCharsets.UTF_8))) {
            requireStatus(() -> host.start(30_000), WamrException.MISMATCH);
        }
    }

    static void denial() throws Exception {
        AtomicBoolean denyNext = new AtomicBoolean(true);
        try (WamrPluginHost host = started(null, request -> !denyNext.getAndSet(false))) {
            requireStatus(() -> host.invoke(request("echo", "1"), 30_000), WamrException.DENIED);
            require(text(host.invoke(request("echo", "2"), 30_000)).contains("\"payload\":2"), "second call");
        }
    }

    static void deadline() throws Exception {
        // The host enforces the request deadline itself: a guest still running at the deadline is
        // stopped and the instance ends, so the caller sees a timeout. The guest also watches its
        // own deadline, so at the boundary it may answer first, with an error and no result.
        try (WamrPluginHost host = started()) {
            Instant began = Instant.now();
            byte[] call = request("wait", null, "1", Instant.now().plusMillis(400));
            try {
                String reply = text(host.invoke(call, 10_000));
                require(reply.contains("\"error\"") && !reply.contains("\"payload\""), reply);
            } catch (WamrException e) {
                require(e.status() == WamrException.TIMEOUT, e.toString());
                requireFails(() -> host.invoke(request("echo", "1"), 30_000));
            }
            long elapsed = Duration.between(began, Instant.now()).toMillis();
            require(elapsed > 300 && elapsed < 5000, "elapsed " + elapsed + " ms");
        }
    }

    static void timeout() throws Exception {
        try (WamrPluginHost host = started()) {
            Instant began = Instant.now();
            byte[] call = request("wait", null, "1", Instant.now().plusSeconds(120));
            requireStatus(() -> host.invoke(call, 300), WamrException.TIMEOUT);
            require(Duration.between(began, Instant.now()).toMillis() < 5000, "the interpreter must stop at the deadline");
            requireFails(() -> host.invoke(request("echo", "1"), 30_000));
        }
    }

    static void closeInterrupts() throws Exception {
        WamrPluginHost host = started();
        Thread closer = new Thread(() -> {
            try {
                Thread.sleep(300);
            } catch (InterruptedException ignored) {
                return;
            }
            host.close();
        });
        closer.start();
        Instant began = Instant.now();
        byte[] call = request("wait", null, "1", Instant.now().plusSeconds(120));
        requireFails(() -> host.invoke(call, 60_000));
        require(Duration.between(began, Instant.now()).toMillis() < 5000, "close must interrupt the call");
        closer.join();
        host.close();
    }

    static void notReactor() throws Exception {
        // A valid module exporting only _start: a command.
        byte[] command = {0, 97, 115, 109, 1, 0, 0, 0, 1, 4, 1, 96, 0, 0, 3, 2, 1, 0, 7, 10, 1, 6, 95, 115, 116, 97,
                114, 116, 0, 0, 10, 4, 1, 2, 0, 11};
        requireStatus(() -> WamrPluginHost.descriptorOf(command, 30_000, null), WamrException.UNSUPPORTED);
        requireFails(() -> WamrPluginHost.descriptorOf("not a module".getBytes(StandardCharsets.UTF_8), 30_000, null));
        requireFails(() -> new WamrPluginHost(new byte[0], "{}".getBytes(StandardCharsets.UTF_8)));
    }

    static void budget() throws Exception {
        // The handshake needs between 20,000 and 50,000 instructions; the wait loop runs about
        // 150,000 per second, so 200,000 stops it in a second or two.
        try (WamrPluginHost host = started(new WamrPluginHost.Options().instructionLimit(200_000), null)) {
            Instant began = Instant.now();
            byte[] call = request("wait", null, "1", Instant.now().plusSeconds(120));
            requireFails(() -> host.invoke(call, 30_000));
            require(Duration.between(began, Instant.now()).toMillis() < 10_000, "the budget, not the timeout, must stop the call");
            requireFails(() -> host.invoke(request("echo", "1"), 30_000));
        }
    }

    static void closedHost() throws Exception {
        WamrPluginHost host = started();
        host.close();
        host.close();
        requireStatus(() -> host.invoke(request("echo", "1"), 30_000), WamrException.CLOSED);
    }
}
