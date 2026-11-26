package io.github.webong.ext.system;

import io.github.webong.ext.system.fixtures.Fixtures;
import io.github.webong.ext.system.fixtures.Json;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Runs testdata/host/v1 and testdata/capability/v1, which the Go and Swift bindings run as well. */
public final class ResourceFixtureTests {
    private static int checks;
    private static int failures;

    private static void check(boolean value, String name) {
        checks++;
        if (!value) {
            failures++;
            System.out.println("FAIL " + name);
        }
    }

    private interface Action { void run(); }

    private static String code(Action action) {
        try {
            action.run();
            return null;
        } catch (IllegalArgumentException error) {
            return "invalid"; // the model's constructors refuse with an IllegalArgumentException, and so does a wrong type
        }
    }

    public static void main(String[] arguments) throws Exception {
        Map<String, Object> file = Fixtures.load("host", 1);
        Set<String> codes = Fixtures.codes();
        for (Object raw : Fixtures.cases(file, "cases")) {
            Map<String, Object> c = Json.object(raw);
            String name = "snapshots/" + c.get("name");
            String want = (String) c.get("code");
            String got = code(() -> build(Json.object(c.get("snapshot"))));
            check(want == null || codes.contains(want), name + ": code " + want + " is not in codes.json");
            check(want == null ? got == null : want.equals(got), name + ": got " + got + ", want " + want);
        }
        for (Object raw : Fixtures.cases(Fixtures.load("capability", 1), "cases")) {
            Map<String, Object> c = Json.object(raw);
            String reason = (String) c.get("reason");
            String want = (String) c.get("code");
            String got = code(() -> new ResourceCollectionStatus((String) c.get("state"), reason, null, null));
            check(want == null ? got == null : want.equals(got), "capability/" + c.get("state") + (reason == null ? "" : "+" + reason));
        }
        System.out.println(checks + " checks, " + failures + " failures");
        if (failures != 0) {
            System.exit(1);
        }
    }

    // Reads a snapshot in its wire form into the model. A value of the wrong type is an IllegalArgumentException.

    private static long integer(Object value) {
        if (!(value instanceof Long)) throw new IllegalArgumentException("expected an integer");
        return (Long) value;
    }

    private static double number(Object value) {
        if (value instanceof Long) return (Long) value;
        if (!(value instanceof Double)) throw new IllegalArgumentException("expected a number");
        return (Double) value;
    }

    private static String string(Object value) {
        if (!(value instanceof String)) throw new IllegalArgumentException("expected a string");
        return (String) value;
    }

    private static ResourceCollectionStatus status(Object value) {
        Map<String, Object> m = Json.object(value);
        return new ResourceCollectionStatus(string(m.get("state")), (String) m.get("reason"), (String) m.get("detail"), (String) m.get("observed_at"));
    }

    private static ResourceInfo build(Map<String, Object> s) {
        List<StorageInfo> storage = new ArrayList<>();
        if (s.containsKey("storage")) {
            for (Object raw : Json.array(s.get("storage"))) {
                Map<String, Object> m = Json.object(raw);
                storage.add(new StorageInfo(string(m.get("device")), string(m.get("mount_point")), integer(m.get("total_bytes")),
                        integer(m.get("available_bytes")), (String) m.get("type"), Boolean.TRUE.equals(m.get("read_only"))));
            }
        }
        PowerInfo power = null;
        if (s.containsKey("power")) {
            Map<String, Object> m = Json.object(s.get("power"));
            power = new PowerInfo(string(m.get("state")), (int) integer(m.get("percentage")),
                    m.containsKey("time_remaining_seconds") ? integer(m.get("time_remaining_seconds")) : null);
        }
        Map<String, Object> c = Json.object(s.get("container"));
        List<String> evidence = new ArrayList<>();
        for (Object e : Json.array(c.get("evidence"))) {
            evidence.add(string(e));
        }
        ContainerInfo container = new ContainerInfo(Boolean.TRUE.equals(c.get("detected")), string(c.get("state")), evidence);
        return new ResourceInfo(string(s.get("os")), string(s.get("architecture")), string(s.get("observed_at")),
                (int) integer(s.get("logical_cpus")), number(s.get("cpu_capacity")), status(s.get("cpu_status")),
                integer(s.get("memory_bytes")), integer(s.get("memory_capacity_bytes")), status(s.get("memory_status")),
                storage, power, container,
                s.containsKey("uptime_seconds") ? number(s.get("uptime_seconds")) : null,
                s.containsKey("boot_time") ? string(s.get("boot_time")) : null, status(s.get("uptime_status")));
    }

    private ResourceFixtureTests() {}
}
