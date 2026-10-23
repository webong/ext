package io.github.webong.ext.plugin;

import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * The shared fixtures for progress events and reverse calls (lib/plugin/testdata/v1), which the
 * Go contract computes and every SDK reproduces: frames, the sender's rate limiter and the wire
 * model. Run from GuestTests, from the repository root as scripts/plugin-android.sh does.
 */
final class FramesTests {
    private FramesTests() {}

    private static byte[] file(String name) throws Exception {
        Path path = Path.of(System.getProperty("ext.fixtures", "lib/plugin/testdata/v1"), name);
        return Files.readAllBytes(path);
    }

    private static Map<String, Object> fixture(String name) throws Exception {
        return Json.object(Json.parse(file(name)));
    }

    private static List<Map<String, Object>> cases(Object list) {
        List<Map<String, Object>> out = new ArrayList<>();
        for (Object item : (List<?>) list) {
            out.add(Json.object(item));
        }
        return out;
    }

    private static String text(Map<String, Object> m, String key) {
        return (String) m.get(key);
    }

    private static long number(Map<String, Object> m, String key) {
        return m.get(key) instanceof Json.Num n ? Long.parseLong(n.text()) : 0;
    }

    private static boolean flag(Map<String, Object> m, String key) {
        return Boolean.TRUE.equals(m.get(key));
    }

    /** Collects the first few mismatches so a failure says what diverged. */
    private static final class Mismatches {
        private final List<String> first = new ArrayList<>();
        private int count;

        void note(String section, Map<String, Object> c, String what) {
            count++;
            if (first.size() < 12) {
                first.add(section + ": " + text(c, "name") + ": " + what);
            }
        }

        void finish() {
            if (count != 0) {
                throw new AssertionError(count + " mismatches, the first " + first.size() + ":\n" + String.join("\n", first));
            }
        }
    }

    interface Judge {
        void judge(byte[] frame, String id) throws Frames.Violation;
    }

    static boolean accepts(Judge judge, byte[] frame, String id) {
        try {
            judge.judge(frame, id);
            return true;
        } catch (Frames.Violation e) {
            return false;
        }
    }

    static void frames() throws Exception {
        Map<String, Object> root = fixture("frame-cases.json");
        Mismatches bad = new Mismatches();
        int total = 0;
        Object[][] sections = {
            {"progress", (Judge) (frame, id) -> Frames.progressFrame(Frames.decode(frame))},
            {"reverseRequest", (Judge) (frame, id) -> Frames.reverseRequest(Frames.decode(frame))},
            {"reverseResponse", (Judge) (frame, id) -> Frames.reverseResponse(Frames.decode(frame), id)},
        };
        for (Object[] section : sections) {
            String name = (String) section[0];
            List<Map<String, Object>> list = cases(root.get(name));
            int valid = (int) list.stream().filter(c -> flag(c, "valid")).count();
            GuestTests.require(list.size() >= 20 && valid >= 5 && list.size() - valid >= 5, name + " proves little");
            for (Map<String, Object> c : list) {
                total++;
                String id = c.get("id") instanceof String s ? s : "";
                boolean got = accepts((Judge) section[1], text(c, "frame").getBytes(StandardCharsets.UTF_8), id);
                if (got != flag(c, "valid")) {
                    bad.note(name, c, "accepted " + got + ": " + text(c, "frame"));
                }
            }
        }
        GuestTests.require(total >= 500, "only " + total + " frame cases");
        bad.finish();
    }

    static void limiter() throws Exception {
        List<Map<String, Object>> list = cases(fixture("progress-limiter-cases.json").get("cases"));
        GuestTests.require(list.size() >= 10, "limiter cases");
        Mismatches bad = new Mismatches();
        for (Map<String, Object> c : list) {
            Frames.Limiter limiter = new Frames.Limiter(number(c, "eventsPerSecond"), number(c, "bytesPerSecond"), 0);
            int i = 0;
            for (Map<String, Object> step : cases(c.get("steps"))) {
                long got = limiter.admit(number(step, "textBytes"), number(step, "atMs") * 1_000_000L);
                boolean want = flag(step, "admitted");
                if ((got >= 0) != want || (want && got != number(step, "dropped"))) {
                    bad.note("limiter", c, "step " + i + ": " + got + ", want " + want + " " + number(step, "dropped"));
                }
                i++;
            }
            if (limiter.pending() != number(c, "pending")) {
                bad.note("limiter", c, "pending count");
            }
        }
        bad.finish();
    }

    /** Whether the engine takes a descriptor. */
    private static boolean accepted(byte[] descriptor) {
        try (Guest guest = new Guest(descriptor, call -> null)) {
            return guest.descriptorJson().length > 0;
        } catch (GuestException e) {
            return false;
        }
    }

    static void model() throws Exception {
        byte[] bytes = file("model-cases.json");
        Map<String, Object> root = Json.object(Json.parse(bytes));
        Mismatches bad = new Mismatches();
        List<Map<String, Object>> descriptors = cases(root.get("descriptors"));
        GuestTests.require(descriptors.size() > 100, "descriptor cases");
        for (Map<String, Object> c : descriptors) {
            boolean got = accepted(text(c, "descriptor").getBytes(StandardCharsets.UTF_8));
            if (got != flag(c, "valid")) {
                bad.note("descriptor", c, "accepted " + got + ": " + text(c, "descriptor"));
            }
        }
        // The descriptor the requests are judged against.
        byte[] selected = Json.member(bytes, "requestDescriptor");
        List<Map<String, Object>> requests = cases(root.get("requests"));
        GuestTests.require(requests.size() > 100, "request cases");
        for (Map<String, Object> c : requests) {
            ProgressLimits[] seen = new ProgressLimits[1];
            boolean[] ran = new boolean[1];
            boolean got;
            try (Guest guest = new Guest(selected, call -> {
                ran[0] = true;
                seen[0] = call.progressLimits();
                return null;
            })) {
                Connection connection = new Connection(guest.declared(), OutputStream.nullOutputStream());
                try {
                    guest.invoke(text(c, "request").getBytes(StandardCharsets.UTF_8), 5000, connection);
                } catch (GuestException e) {
                    // A request the engine will not take at all.
                }
                got = ran[0];
            }
            if (got != flag(c, "valid")) {
                bad.note("request", c, "accepted " + got + ": " + text(c, "request"));
                continue;
            }
            if (!got) {
                continue;
            }
            // The call gets a reporter when its operation declared progress and the request carries
            // a rate; the rate has its defaults filled in.
            if ((seen[0] != null) != flag(c, "reporter")) {
                bad.note("request", c, "reporter " + (seen[0] != null));
            } else if (seen[0] != null
                    && (seen[0].eventsPerSecond() != number(c, "events") || seen[0].bytesPerSecond() != number(c, "bytes"))) {
                bad.note("request", c, "rate " + seen[0]);
            }
        }
        bad.finish();
    }
}
