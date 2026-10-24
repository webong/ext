package io.github.webong.ext.plugin;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

/**
 * The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
 * docs/plugin-guest-frames.md): reports progress and calls its host. It serves standard input
 * and output as a JSON-line guest process, so the shared Go suite can check it.
 */
public final class FramesGuest {
    private FramesGuest() {}

    private static final ContractRef HOST = new ContractRef("host.svc", "v1");
    /** Outcomes of session-scoped calls, from background work to "collect". */
    private static final BlockingQueue<String> BACKGROUND = new LinkedBlockingQueue<>();

    public static void main(String[] args) throws Exception {
        List<Operation> reverse = List.of(new Operation("sign", "signing"), new Operation("secret"), new Operation("empty"),
                new Operation("fail"), new Operation("errok"), new Operation("errretryok"), new Operation("big"),
                new Operation("hold"));
        Descriptor descriptor = new Descriptor(
                new Identity("ctx/frames", "fixture-1"),
                List.of(new Contract("ext.frames", "v1", List.of(
                        Operation.reporting("stream"), Operation.reporting("flood"), Operation.reporting("limits"),
                        Operation.reporting("split"), new Operation("plain"), new Operation("call"),
                        new Operation("twice"), new Operation("idle"), new Operation("collect"), new Operation("burst")))),
                List.of(new Contract("host.svc", "v1", reverse)));
        try (Guest guest = new Guest(descriptor, FramesGuest::handle)) {
            JsonLine.serveStdio(guest);
        }
    }

    private static String quoted(String s) {
        return Json.quote(new StringBuilder(), s).toString();
    }

    private static byte[] bytes(String s) {
        return s.getBytes(java.nio.charset.StandardCharsets.UTF_8);
    }

    /** How a reverse call came out, in the shape the suite reads. */
    private static String outcome(ReverseCaller caller, String operation, byte[] payload, Duration timeout) {
        try {
            byte[] result = timeout == null ? caller.call(HOST, operation, payload) : caller.call(HOST, operation, payload, timeout);
            return "{\"result\":" + new String(result, java.nio.charset.StandardCharsets.UTF_8) + "}";
        } catch (RemoteError e) {
            return "{\"error\":{\"code\":" + quoted(e.code()) + ",\"message\":" + quoted(e.getMessage())
                    + (e.retryAfterMilliseconds() != 0 ? ",\"retry\":" + e.retryAfterMilliseconds() : "") + "}}";
        } catch (FrameException e) {
            String other = switch (e.code()) {
                case CLOSED -> "closed";
                case DEADLINE -> "deadline";
                case CANCELED -> "canceled";
                default -> "failed";
            };
            return "{\"error\":{\"other\":\"" + other + "\"}}";
        }
    }

    private static Map<String, Object> input(Call call) {
        byte[] payload = call.payload();
        return payload.length == 0 ? Map.of() : orEmpty(Json.object(Json.parse(payload)));
    }

    private static Map<String, Object> orEmpty(Map<String, Object> map) {
        return map == null ? Map.of() : map;
    }

    static byte[] handle(Call call) throws Exception {
        Map<String, Object> input = input(call);
        switch (call.operation()) {
            case "stream":
                report(call, Progress.fraction(1, 3));
                report(call, Progress.out("hello"));
                report(call, Progress.err("oops"));
                return bytes("{\"done\":true}");
            case "flood": {
                long count = input.get("count") instanceof Json.Num n ? Long.parseLong(n.text()) : 300;
                long dropped = 0;
                for (long i = 0; i < count; i++) {
                    if (report(call, Progress.log("x")) == ProgressOutcome.DROPPED) {
                        dropped++;
                    }
                }
                return bytes("{\"dropped\":" + dropped + "}");
            }
            case "limits": {
                ProgressLimits limits = call.progressLimits();
                return bytes("{\"declared\":" + (limits != null) + ",\"events\":" + (limits == null ? 0 : limits.eventsPerSecond())
                        + ",\"bytes\":" + (limits == null ? 0 : limits.bytesPerSecond()) + "}");
            }
            case "split": {
                if (!(input.get("text") instanceof String text)) {
                    throw new IllegalArgumentException("text is required");
                }
                List<String> pieces = Progress.split(text);
                for (String piece : pieces) {
                    report(call, Progress.out(piece));
                }
                return bytes("{\"pieces\":" + pieces.size() + "}");
            }
            case "plain": {
                boolean unsupported = false;
                try {
                    call.progress(Progress.log("never sent"));
                } catch (FrameException e) {
                    unsupported = e.code() == FrameException.Code.UNSUPPORTED;
                }
                return bytes("{\"unsupported\":" + unsupported + ",\"limits\":" + (call.progressLimits() != null) + "}");
            }
            case "call": {
                ReverseCaller reverse = call.reverse();
                if (reverse == null) {
                    return bytes("{\"error\":{\"other\":\"unsupported\"}}");
                }
                if (!(input.get("op") instanceof String op)) {
                    throw new IllegalArgumentException("op is required");
                }
                return bytes(outcome(reverse, op, Json.member(call.payload(), "payload"), null));
            }
            case "twice": {
                ReverseCaller reverse = call.reverse();
                String first = outcome(reverse, "sign", bytes("1"), null);
                String second = outcome(reverse, "sign", bytes("2"), null);
                if (!first.startsWith("{\"result\":") || !second.startsWith("{\"result\":")) {
                    throw new IllegalStateException("a reverse call failed");
                }
                return bytes("{\"first\":" + first.substring(10, first.length() - 1) + ",\"second\":"
                        + second.substring(10, second.length() - 1) + "}");
            }
            case "idle": {
                ReverseCaller session = call.sessionReverse();
                Thread background = new Thread(() -> {
                    try {
                        Thread.sleep(20);
                    } catch (InterruptedException e) {
                        return;
                    }
                    BACKGROUND.add(outcome(session, "sign", bytes("{\"n\":1}"), Duration.ofSeconds(10)));
                }, "background");
                background.setDaemon(true);
                background.start();
                return bytes("{\"started\":true}");
            }
            case "collect": {
                String outcome = BACKGROUND.poll(5, TimeUnit.SECONDS);
                return bytes(outcome == null ? "{\"timeout\":true}" : outcome);
            }
            case "burst": {
                ReverseCaller reverse = call.reverse();
                Map<String, Integer> counts = new TreeMap<>();
                Thread[] threads = new Thread[6];
                for (int i = 0; i < threads.length; i++) {
                    threads[i] = new Thread(() -> {
                        String key = "ok";
                        try {
                            reverse.call(HOST, "hold", null);
                        } catch (RemoteError e) {
                            key = e.code();
                        } catch (FrameException e) {
                            key = "other";
                        }
                        synchronized (counts) {
                            counts.merge(key, 1, Integer::sum);
                        }
                    });
                    threads[i].start();
                }
                for (Thread thread : threads) {
                    thread.join();
                }
                StringBuilder json = new StringBuilder("{");
                for (Map.Entry<String, Integer> entry : counts.entrySet()) {
                    json.append(json.length() > 1 ? "," : "");
                    Json.quote(json, entry.getKey()).append(':').append(entry.getValue());
                }
                return bytes(json.append('}').toString());
            }
            default:
                throw new IllegalStateException("unknown operation");
        }
    }

    /** Reports an event; a refusal is not the handler's failure. */
    private static ProgressOutcome report(Call call, Progress event) {
        try {
            return call.progress(event);
        } catch (FrameException e) {
            return null;
        }
    }
}
