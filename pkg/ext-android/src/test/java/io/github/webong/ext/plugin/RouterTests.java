package io.github.webong.ext.plugin;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

/**
 * The router behind {@link JsonLine#serve}: progress events, reverse calls and the rules a host
 * that breaks them meets. The host is a test thread on in-memory pipes. Run from GuestTests.
 */
final class RouterTests {
    private RouterTests() {}

    static final ContractRef CONTRACT = new ContractRef("test.frames", "v1");
    static final ContractRef HOST = new ContractRef("host.svc", "v1");

    static final Descriptor DESCRIPTOR = new Descriptor(
            new Identity("java/frames", "r1"),
            List.of(new Contract("test.frames", "v1", List.of(
                    Operation.reporting("events"), Operation.reporting("flood"), Operation.reporting("invalid"),
                    new Operation("quiet"), new Operation("ask"), new Operation("twice"), new Operation("late"),
                    new Operation("hold"), new Operation("session"), new Operation("stale")))),
            List.of(new Contract("host.svc", "v1", List.of(new Operation("sign")))));

    private static String quoted(String s) {
        return Json.quote(new StringBuilder(), s).toString();
    }

    private static byte[] bytes(String s) {
        return s.getBytes(StandardCharsets.UTF_8);
    }

    private static String text(byte[] b) {
        return new String(b, StandardCharsets.UTF_8);
    }

    /** How a reverse call came out, as a JSON member. */
    private static String attempt(ReverseCaller caller, String operation, byte[] payload, Duration timeout) {
        try {
            byte[] result = timeout == null ? caller.call(HOST, operation, payload) : caller.call(HOST, operation, payload, timeout);
            return "{\"result\":" + text(result) + "}";
        } catch (RemoteError e) {
            return "{\"code\":" + quoted(e.code()) + "}";
        } catch (FrameException e) {
            return "{\"frame\":" + quoted(e.code().name().toLowerCase(java.util.Locale.ROOT)) + "}";
        }
    }

    /** What the "stale" operation kept of its call. */
    static volatile ReverseCaller kept;

    static byte[] handle(Call call) throws Exception {
        switch (call.operation()) {
            case "stale":
                kept = call.reverse();
                return null;
            case "events": {
                boolean first = call.progress(Progress.log("a")) == ProgressOutcome.SENT;
                boolean second = call.progress(Progress.fraction(1, 2)) == ProgressOutcome.SENT;
                return bytes("{\"sent\":[" + first + "," + second + "]}");
            }
            case "flood": {
                int dropped = 0;
                for (int i = 0; i < 300; i++) {
                    if (call.progress(Progress.log("x")) == ProgressOutcome.DROPPED) {
                        dropped++;
                    }
                }
                return bytes("{\"dropped\":" + dropped + "}");
            }
            case "quiet": {
                try {
                    call.progress(Progress.log("x"));
                    return bytes("{\"unsupported\":false}");
                } catch (FrameException e) {
                    return bytes("{\"unsupported\":" + (e.code() == FrameException.Code.UNSUPPORTED) + "}");
                }
            }
            case "invalid": {
                List<String> codes = new ArrayList<>();
                for (Progress event : List.of(new Progress("mystery", "", "", 0, 0), Progress.fraction(3, 2),
                        Progress.log("x".repeat(2049)), new Progress("log", "both", "", 0, 0),
                        Progress.log("\ud800"), new Progress("progress", "out", "", 0, 0))) {
                    try {
                        call.progress(event);
                        codes.add("\"sent\"");
                    } catch (FrameException e) {
                        codes.add(quoted(e.code().name().toLowerCase(java.util.Locale.ROOT)));
                    }
                }
                return bytes("{\"codes\":[" + String.join(",", codes) + "]}");
            }
            case "ask": {
                Map<String, Object> input = Json.object(Json.parse(call.payload()));
                String op = Json.string(input.get("op"));
                byte[] payload = Json.member(call.payload(), "payload");
                return bytes(attempt(call.reverse(), op == null ? "sign" : op, payload, null));
            }
            case "twice": {
                String[] first = new String[1];
                Thread other = new Thread(() -> first[0] = attempt(call.reverse(), "sign", bytes("1"), null));
                other.start();
                String second = attempt(call.reverse(), "sign", bytes("2"), null);
                other.join();
                return bytes("{\"both\":[" + first[0] + "," + second + "]}");
            }
            case "late":
                return bytes(attempt(call.reverse(), "sign", null, Duration.ofMillis(100)));
            case "hold":
                while (!call.connectionEnded()) {
                    Thread.sleep(2);
                }
                return null;
            case "session": {
                ReverseCaller session = call.sessionReverse();
                String answer = session == null ? "null" : attempt(session, "sign", null, null);
                return bytes("{\"session\":" + (session != null) + ",\"reverse\":" + (call.reverse() != null)
                        + ",\"answer\":" + answer + "}");
            }
            default:
                throw new IllegalStateException(call.operation());
        }
    }

    /** One direction of an in-memory connection. */
    static final class Pipe {
        private static final byte[] END = new byte[0];
        private final LinkedBlockingQueue<byte[]> queue = new LinkedBlockingQueue<>();
        private byte[] current = END;
        private int position;
        private boolean ended;

        final InputStream in = new InputStream() {
            @Override
            public int read() throws IOException {
                while (position >= current.length) {
                    if (ended) {
                        return -1;
                    }
                    try {
                        byte[] next = queue.take();
                        if (next == END) {
                            ended = true;
                            return -1;
                        }
                        current = next;
                        position = 0;
                    } catch (InterruptedException e) {
                        throw new java.io.InterruptedIOException();
                    }
                }
                return current[position++] & 0xff;
            }
        };

        final OutputStream out = new OutputStream() {
            @Override
            public void write(int b) {
                queue.add(new byte[] {(byte) b});
            }

            @Override
            public void write(byte[] b, int off, int len) {
                queue.add(java.util.Arrays.copyOfRange(b, off, off + len));
            }
        };

        void put(byte[] bytes) {
            queue.add(bytes);
        }

        void end() {
            queue.add(END);
        }
    }

    /** The host: serves a guest on pipes and reads what it writes. */
    static final class Host {
        private final Pipe toGuest = new Pipe();
        private final Pipe fromGuest = new Pipe();
        private final LinkedBlockingQueue<String> frames = new LinkedBlockingQueue<>();
        private final Thread served;
        private final Thread reader;
        private volatile Throwable outcome;
        private int next;

        Host() throws Exception {
            this(true);
        }

        Host(boolean hello) throws Exception {
            Guest guest = new Guest(DESCRIPTOR, RouterTests::handle);
            served = new Thread(() -> {
                try (guest) {
                    JsonLine.serve(guest, toGuest.in, fromGuest.out);
                } catch (Throwable t) {
                    outcome = t;
                } finally {
                    fromGuest.end();
                }
            }, "served");
            served.start();
            reader = new Thread(() -> {
                ByteArrayOutputStream line = new ByteArrayOutputStream();
                try {
                    int b;
                    while ((b = fromGuest.in.read()) >= 0) {
                        if (b == '\n') {
                            frames.add(text(line.toByteArray()));
                            line.reset();
                        } else {
                            line.write(b);
                        }
                    }
                } catch (IOException e) {
                    outcome = e;
                }
            }, "host-reader");
            reader.setDaemon(true);
            reader.start();
            if (hello) {
                send(hello(null));
            }
        }

        static String hello(String extra) {
            return "{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"operation\":\"plugin.hello\",\"deadline\":\""
                    + Instant.now().plusSeconds(30) + "\"" + (extra == null ? "" : "," + extra) + "}";
        }

        void send(String frame) {
            toGuest.put(bytes(frame + "\n"));
        }

        /** The next frame the guest wrote, or null when none comes within {@code millis}. */
        String tryRecv(long millis) throws InterruptedException {
            return frames.poll(millis, TimeUnit.MILLISECONDS);
        }

        Map<String, Object> recv() throws Exception {
            String frame = tryRecv(5000);
            GuestTests.require(frame != null, "the guest wrote nothing");
            return Json.object(Json.parse(bytes(frame)));
        }

        /** A request for {@code operation}; a call whose operation reports progress asks for events. */
        String request(String operation, String payload, boolean progress) {
            String id = Integer.toString(++next);
            send("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"" + id + "\",\"plugin\":{\"id\":\"java/frames\",\"revision\":\"r1\"},"
                    + "\"contract\":{\"name\":\"test.frames\",\"version\":\"v1\"},\"operation\":\"" + operation + "\","
                    + "\"payload\":" + payload + ",\"deadline\":\"" + Instant.now().plusSeconds(30) + "\""
                    + (progress ? ",\"progress\":{\"eventsPerSecond\":5}" : "") + "}");
            return id;
        }

        /** Reads frames up to the response to {@code id}: those before it, and it. */
        record Exchange(List<Map<String, Object>> before, Map<String, Object> response) {}

        Exchange untilResponse(String id) throws Exception {
            List<Map<String, Object>> before = new ArrayList<>();
            while (true) {
                Map<String, Object> frame = recv();
                if (!frame.containsKey("frame") && id.equals(frame.get("id"))) {
                    return new Exchange(before, frame);
                }
                before.add(frame);
            }
        }

        /** Ends the host's side and waits for the guest to end; what the session threw, or null. */
        Throwable finish() throws Exception {
            toGuest.end();
            served.join(10_000);
            GuestTests.require(!served.isAlive(), "the guest did not end");
            return outcome;
        }
    }

    static Host started() throws Exception {
        Host host = new Host();
        GuestTests.require("hello".equals(host.recv().get("id")), "hello");
        return host;
    }

    static Map<String, Object> payload(Map<String, Object> response) {
        return Json.object(response.get("payload"));
    }

    static String answer(String id, String body) {
        return "{\"frame\":\"reverse_response\",\"apiVersion\":\"ext.plugin/v1\",\"id\":\"" + id + "\"" + (body.isEmpty() ? "" : "," + body) + "}";
    }

    static void require(boolean ok, String message) {
        GuestTests.require(ok, message);
    }

    static void descriptor() throws Exception {
        Host host = new Host();
        Map<String, Object> hello = host.recv();
        String served = hello.toString();
        require(served.contains("name=events, progress=true") && !served.contains("name=quiet, progress"), served);
        require(served.contains("reverse=[{name=host.svc"), served);
        require(host.finish() == null, "session ended badly");
    }

    static void eventsInOrder() throws Exception {
        Host host = started();
        String id = host.request("events", "null", true);
        Host.Exchange x = host.untilResponse(id);
        require(x.before().size() == 2, x.before().toString());
        for (Map<String, Object> event : x.before()) {
            require("progress".equals(event.get("frame")) && id.equals(event.get("parent")), event.toString());
        }
        require("log".equals(x.before().get(0).get("kind")) && "a".equals(x.before().get(0).get("text")), x.before().get(0).toString());
        require("progress".equals(x.before().get(1).get("kind")) && x.before().get(1).get("completed") instanceof Json.Num n
                && n.text().equals("1") && x.before().get(1).get("total") instanceof Json.Num t && t.text().equals("2"),
                x.before().get(1).toString());
        require(x.response().toString().contains("sent=[true, true]"), x.response().toString());
        require(host.tryRecv(100) == null, "a frame after the response");
        require(host.finish() == null, "session ended badly");
    }

    static void flood() throws Exception {
        Host host = started();
        String id = host.request("flood", "null", true);
        Host.Exchange x = host.untilResponse(id);
        long dropped = Long.parseLong(((Json.Num) payload(x.response()).get("dropped")).text());
        long delivered = x.before().stream().filter(e -> e.containsKey("text")).count();
        long reported = x.before().stream().mapToLong(e -> e.get("dropped") instanceof Json.Num n ? Long.parseLong(n.text()) : 0).sum();
        require(delivered >= 10 && delivered + dropped == 300 && reported == dropped,
                delivered + " delivered, " + dropped + " dropped, " + reported + " reported");
        // The drops still unreported when the handler returned are in a marker with only a kind and a count.
        Map<String, Object> last = x.before().get(x.before().size() - 1);
        require(!last.containsKey("text") && "log".equals(last.get("kind")) && last.get("dropped") instanceof Json.Num n
                && Long.parseLong(n.text()) > 0, last.toString());
        require(host.finish() == null, "session ended badly");
    }

    static void undeclared() throws Exception {
        Host host = started();
        String id = host.request("quiet", "null", false);
        Host.Exchange x = host.untilResponse(id);
        require(x.before().isEmpty(), x.before().toString());
        require(Boolean.TRUE.equals(payload(x.response()).get("unsupported")), x.response().toString());
        // A rate the host sets for an operation that did not declare progress is not a reporter either.
        id = host.request("quiet", "null", true);
        x = host.untilResponse(id);
        require(x.before().isEmpty() && Boolean.TRUE.equals(payload(x.response()).get("unsupported")), x.response().toString());
        require(host.finish() == null, "session ended badly");
    }

    static void invalidEvents() throws Exception {
        Host host = started();
        String id = host.request("invalid", "null", true);
        Host.Exchange x = host.untilResponse(id);
        require(x.before().isEmpty(), x.before().toString());
        require(payload(x.response()).get("codes").toString().equals("[invalid, invalid, invalid, invalid, invalid, invalid]"),
                x.response().toString());
        require(host.finish() == null, "session ended badly");
    }

    static void reverseCall() throws Exception {
        Host host = started();
        String id = host.request("ask", "{\"op\":\"sign\",\"payload\":{\"n\":1.10}}", false);
        Map<String, Object> request = host.recv();
        require("reverse_request".equals(request.get("frame")) && "g1".equals(request.get("id"))
                && id.equals(request.get("parent")) && "sign".equals(request.get("operation")), request.toString());
        require(request.get("contract").toString().equals("{name=host.svc, version=v1}"), request.toString());
        require(request.get("payload").toString().equals("{n=Num[text=1.10]}"), request.toString());
        host.send(answer("g1", "\"payload\":\"signed\""));
        require(host.untilResponse(id).response().toString().contains("result=signed"), "result");
        // The host's error comes back with its code, and ids go on counting.
        id = host.request("ask", "{\"op\":\"sign\"}", false);
        request = host.recv();
        require("g2".equals(request.get("id")) && !request.containsKey("payload"), request.toString());
        host.send(answer("g2", "\"error\":{\"code\":\"denied\",\"message\":\"reverse call refused\"}"));
        require(host.untilResponse(id).response().toString().contains("code=denied"), "denied");
        // An operation the descriptor does not list is refused without a frame.
        id = host.request("ask", "{\"op\":\"nothere\"}", false);
        Host.Exchange x = host.untilResponse(id);
        require(x.before().isEmpty() && x.response().toString().contains("code=reverse_undeclared"), x.response().toString());
        require(host.finish() == null, "session ended badly");
    }

    static void reverseAtOnce() throws Exception {
        Host host = started();
        String id = host.request("twice", "null", false);
        Map<String, Object> first = host.recv();
        Map<String, Object> second = host.recv();
        require(List.of("g1", "g2").containsAll(List.of((String) first.get("id"), (String) second.get("id")))
                && !first.get("id").equals(second.get("id")), first + " " + second);
        // Answered in the other order, each with the payload of the request it answers.
        for (Map<String, Object> request : List.of(second, first)) {
            host.send(answer((String) request.get("id"), "\"payload\":\"" + request.get("payload").toString().replace("Num[text=", "").replace("]", "") + "\""));
        }
        String done = host.untilResponse(id).response().toString();
        require(done.contains("result=1") && done.contains("result=2"), done);
        require(host.finish() == null, "session ended badly");
    }

    static void sessionScoped() throws Exception {
        Host host = started();
        String id = host.request("session", "null", false);
        Map<String, Object> request = host.recv();
        require(!request.containsKey("parent"), request.toString());
        host.send(answer((String) request.get("id"), "\"payload\":\"ok\""));
        Map<String, Object> payload = payload(host.untilResponse(id).response());
        require(Boolean.TRUE.equals(payload.get("session")) && Boolean.TRUE.equals(payload.get("reverse"))
                && payload.get("answer").toString().contains("result=ok"), payload.toString());
        require(host.finish() == null, "session ended badly");
    }

    static void callScopeEnds() throws Exception {
        Host host = started();
        String id = host.request("stale", "null", false);
        host.untilResponse(id);
        // A request names the call it belongs to, so a caller kept past the call writes nothing.
        try {
            kept.call(HOST, "sign", null);
            throw new AssertionError("a caller outlived its call");
        } catch (FrameException e) {
            require(e.code() == FrameException.Code.CLOSED, "code " + e.code());
        }
        require(host.tryRecv(100) == null, "a frame for a call that is over");
        require(host.finish() == null, "session ended badly");
    }

    static void abandoned() throws Exception {
        Host host = started();
        String id = host.request("late", "null", false);
        Map<String, Object> request = host.recv();
        require("g1".equals(request.get("id")), request.toString());
        require(host.untilResponse(id).response().toString().contains("frame=deadline"), "deadline");
        // The late answer is dropped; the session goes on.
        host.send(answer("g1", "\"payload\":1"));
        String next = host.request("events", "null", true);
        require(host.untilResponse(next).response().toString().contains("sent=[true, true]"), "next call");
        require(host.finish() == null, "session ended badly");
    }

    static void violations() throws Exception {
        String[][] violations = {
            {"a second request during a call", null},
            {"an unknown frame", "{\"frame\":\"mystery\"}"},
            {"an answer to a request never issued", answer("g99", "\"payload\":1")},
            {"a progress frame from the host", "{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\",\"parent\":\"1\",\"kind\":\"log\"}"},
            {"a malformed answer to the request waiting", answer("g1", "\"payload\":1,\"extra\":1")},
            {"an answer with both payload and error", answer("g1", "\"payload\":1,\"error\":{\"code\":\"x\",\"message\":\"m\"}")},
            {"an answer with neither payload nor error", answer("g1", "")},
            {"a frame that is not an object", "7"},
            {"an answer to the wrong kind of id", answer("x1", "\"payload\":1")},
        };
        for (String[] violation : violations) {
            Host host = started();
            String id = host.request("ask", "{\"op\":\"sign\"}", false);
            require("g1".equals(host.recv().get("id")), "g1");
            if (violation[1] == null) {
                host.request("events", "null", true);
            } else {
                host.send(violation[1]);
            }
            Throwable failure = host.finish();
            require(failure instanceof IOException && String.valueOf(failure.getMessage()).contains("protocol violation"),
                    violation[0] + ": " + failure);
            // The call that was in flight is never answered.
            for (String frame = host.tryRecv(50); frame != null; frame = host.tryRecv(50)) {
                Map<String, Object> parsed = Json.object(Json.parse(bytes(frame)));
                require(parsed.containsKey("frame") || !id.equals(parsed.get("id")), violation[0] + ": answered " + frame);
            }
        }
    }

    static void connectionEnds() throws Exception {
        Host host = started();
        String id = host.request("ask", "{\"op\":\"sign\"}", false);
        require("g1".equals(host.recv().get("id")), "g1");
        // The host closes its end: the handler waiting for the answer is released, and the call is
        // still answered (the host may be reading), then the session ends.
        Thread finishing = new Thread(() -> {
            try {
                host.finish();
            } catch (Exception ignored) {
                // The assertion below reads the outcome.
            }
        });
        finishing.start();
        Host.Exchange x = host.untilResponse(id);
        require(x.response().toString().contains("frame=closed"), x.response().toString());
        finishing.join();
        require(host.outcome == null, "session ended badly: " + host.outcome);
    }

    static void pipelinedAfterHello() throws Exception {
        Host host = new Host();
        String id = host.request("events", "null", true); // sent before the hello is answered
        require("hello".equals(host.recv().get("id")), "hello");
        require(host.untilResponse(id).response().toString().contains("sent=[true, true]"), "pipelined request");
        require(host.finish() == null, "session ended badly");
    }

    static void withoutFrames() throws Exception {
        try (Guest guest = new Guest(DESCRIPTOR, RouterTests::handle)) {
            for (String[] c : new String[][] {{"events", "operation_failed"}, {"ask", "operation_failed"}}) {
                String response = GuestTests.text(guest.invoke(bytes("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\","
                        + "\"plugin\":{\"id\":\"java/frames\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test.frames\","
                        + "\"version\":\"v1\"},\"operation\":\"" + c[0] + "\",\"deadline\":\"2099-01-01T00:00:00Z\","
                        + "\"payload\":{},\"progress\":{}}")));
                require(response.contains(c[1]), c[0] + ": " + response);
            }
            String response = GuestTests.text(guest.invoke(bytes("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\","
                    + "\"plugin\":{\"id\":\"java/frames\",\"revision\":\"r1\"},\"contract\":{\"name\":\"test.frames\","
                    + "\"version\":\"v1\"},\"operation\":\"session\",\"deadline\":\"2099-01-01T00:00:00Z\"}")));
            require(response.contains("\"session\":false,\"reverse\":false"), response);
        }
    }

    static void bareHello() throws Exception {
        Object[][] cases = {
            {"a bare hello, as the C engine sends it", Host.hello(null), true},
            {"empty members", Host.hello("\"plugin\":{\"id\":\"\",\"revision\":\"\"},\"contract\":{\"name\":\"\",\"version\":\"\"},\"surface\":\"\""), true},
            {"a plugin named", Host.hello("\"plugin\":{\"id\":\"x\",\"revision\":\"\"}"), false},
            {"a surface named", Host.hello("\"surface\":\"x\""), false},
            {"a payload", Host.hello("\"payload\":{}"), false},
        };
        for (Object[] c : cases) {
            Host host = new Host(false);
            host.send((String) c[1]);
            if ((Boolean) c[2]) {
                require("hello".equals(host.recv().get("id")), (String) c[0]);
                require(host.finish() == null, (String) c[0]);
            } else {
                require(host.finish() instanceof IOException, (String) c[0]);
                require(host.tryRecv(50) == null, (String) c[0]);
            }
        }
    }
}
