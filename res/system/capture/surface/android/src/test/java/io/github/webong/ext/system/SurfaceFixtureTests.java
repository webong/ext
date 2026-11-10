package io.github.webong.ext.system;

import io.github.webong.ext.system.fixtures.Fixtures;
import io.github.webong.ext.system.fixtures.Json;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Runs testdata/surface/v1, which the Go and Swift bindings run as well. */
public final class SurfaceFixtureTests {
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

    private static void expect(String name, Set<String> codes, String want, String got) {
        check(want == null || codes.contains(want), name + ": code " + want + " is not in codes.json");
        check(want == null ? got == null : want.equals(got), name + ": got " + got + ", want " + want);
    }

    public static void main(String[] arguments) throws Exception {
        Map<String, Object> file = Fixtures.load("surface", 1);
        Set<String> codes = Fixtures.codes();
        for (Object raw : Fixtures.cases(file, "surface")) {
            Map<String, Object> c = Json.object(raw);
            expect("surface/" + c.get("name"), codes, (String) c.get("code"), code(() -> surface(Json.object(c.get("snapshot")))));
        }
        for (Object raw : Fixtures.cases(file, "permission")) {
            Map<String, Object> c = Json.object(raw);
            expect("permission/" + c.get("name"), codes, (String) c.get("code"), code(() -> permission(Json.object(c.get("snapshot")))));
        }
        for (Object raw : Fixtures.cases(file, "accessibility")) {
            Map<String, Object> c = Json.object(raw);
            expect("accessibility/" + c.get("name"), codes, (String) c.get("code"), code(() -> accessibility(Json.object(c.get("snapshot")))));
        }
        for (Object raw : Fixtures.cases(file, "surfaceOptions")) {
            Map<String, Object> c = Json.object(raw);
            expect("surfaceOptions/" + c.get("name"), codes, (String) c.get("code"),
                    code(() -> new SurfaceOptions((int) integer(c.get("maxDisplays")), (int) integer(c.get("maxWindows")))));
        }
        for (Object raw : Fixtures.cases(file, "accessibilityOptions")) {
            Map<String, Object> c = Json.object(raw);
            expect("accessibilityOptions/" + c.get("name"), codes, (String) c.get("code"),
                    code(() -> new AccessibilityOptions((int) integer(c.get("maxElements")), (int) integer(c.get("maxDepth")), false)));
        }
        Map<String, Object> defaults = Json.object(file.get("defaults"));
        SurfaceOptions surface = new SurfaceOptions();
        Map<String, Object> s = Json.object(defaults.get("surface"));
        check(surface.maxDisplays() == integer(s.get("maxDisplays")) && surface.maxWindows() == integer(s.get("maxWindows")), "surface defaults");
        for (Object raw : Fixtures.cases(Fixtures.load("capability", 1), "cases")) {
            Map<String, Object> c = Json.object(raw);
            String reason = (String) c.get("reason");
            expect("capability/" + c.get("state") + (reason == null ? "" : "+" + reason), codes, (String) c.get("code"),
                    code(() -> new SurfaceCollectionStatus((String) c.get("state"), reason, null, null)));
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

    private static Map<String, Object> required(Map<String, Object> m, String key) {
        if (!m.containsKey(key)) throw new IllegalArgumentException("missing " + key);
        return Json.object(m.get(key));
    }

    private static SurfaceCollectionStatus status(Map<String, Object> m, String key) {
        Map<String, Object> s = required(m, key);
        return new SurfaceCollectionStatus(string(s.get("state")), (String) s.get("reason"), (String) s.get("detail"), (String) s.get("observed_at"));
    }

    private static SurfaceBounds bounds(Object value) {
        Map<String, Object> m = Json.object(value);
        return new SurfaceBounds((int) integer(m.get("x")), (int) integer(m.get("y")), (int) integer(m.get("width")), (int) integer(m.get("height")));
    }

    private static void surface(Map<String, Object> s) {
        List<DisplayInfo> displays = new ArrayList<>();
        for (Object raw : Json.array(s.get("displays"))) {
            Map<String, Object> m = Json.object(raw);
            displays.add(new DisplayInfo(string(m.get("id")), bounds(m.get("bounds")), m.containsKey("scale") ? number(m.get("scale")) : 0,
                    Boolean.TRUE.equals(m.get("primary"))));
        }
        List<WindowInfo> windows = new ArrayList<>();
        for (Object raw : Json.array(s.get("windows"))) {
            Map<String, Object> m = Json.object(raw);
            windows.add(new WindowInfo(string(m.get("id")), (int) integer(m.get("pid")), string(m.get("process_start_id")),
                    bounds(m.get("bounds")), Boolean.TRUE.equals(m.get("on_screen"))));
        }
        new SurfaceSnapshot(string(s.get("platform")), string(s.get("coordinate_space")), string(s.get("observed_at")),
                displays, windows, status(s, "display_status"), status(s, "window_status"));
    }

    private static PermissionInfo permission(Map<String, Object> s, String key) {
        Map<String, Object> m = required(s, key);
        return new PermissionInfo(string(m.get("state")), (String) m.get("reason"), (String) m.get("scope"), (String) m.get("detail"));
    }

    private static void permission(Map<String, Object> s) {
        new PermissionSnapshot(string(s.get("platform")), string(s.get("observed_at")), permission(s, "accessibility"), permission(s, "screen_recording"));
    }

    private static void accessibility(Map<String, Object> s) {
        List<AccessibilityElement> elements = new ArrayList<>();
        for (Object raw : Json.array(s.get("elements"))) {
            Map<String, Object> m = Json.object(raw);
            List<String> actions = new ArrayList<>();
            if (m.containsKey("actions")) {
                for (Object a : Json.array(m.get("actions"))) {
                    actions.add(string(a));
                }
            }
            elements.add(new AccessibilityElement(string(m.get("id")), (String) m.get("parent_id"), (int) integer(m.get("pid")),
                    string(m.get("process_start_id")), string(m.get("role")), (String) m.get("identifier"), (String) m.get("label"),
                    (String) m.get("value"), m.containsKey("bounds") ? bounds(m.get("bounds")) : null, actions,
                    (Boolean) m.get("enabled"), (Boolean) m.get("focused")));
        }
        new AccessibilitySnapshot(string(s.get("platform")), string(s.get("observed_at")), status(s, "status"), elements);
    }

    private SurfaceFixtureTests() {}
}
