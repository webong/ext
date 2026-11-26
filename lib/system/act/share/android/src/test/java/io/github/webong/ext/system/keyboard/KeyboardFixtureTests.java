package io.github.webong.ext.system.keyboard;

import io.github.webong.ext.system.fixtures.Fixtures;
import io.github.webong.ext.system.fixtures.Json;

import java.util.Map;
import java.util.Set;

/** Runs testdata/keyboard/v1, which the Swift binding runs as well. */
public final class KeyboardFixtureTests {
    private static int checks;
    private static int failures;

    private static void check(boolean value, String name) {
        checks++;
        if (!value) {
            failures++;
            System.out.println("FAIL " + name);
        }
    }

    private static final class Connection implements KeyboardConnection {
        String selection;
        String before;
        String after;

        @Override public CharSequence selectedText() { return selection; }
        @Override public CharSequence textBefore(int characters) { return before; }
        @Override public CharSequence textAfter(int characters) { return after; }
        @Override public boolean insert(CharSequence text) { return true; }
        @Override public boolean deleteBackward(int count) { return true; }
        @Override public boolean deleteForward(int count) { return true; }
        @Override public boolean performEditorAction() { return true; }
    }

    private interface Action { void run() throws Exception; }

    private static String code(Action action) throws Exception {
        try {
            action.run();
            return null;
        } catch (KeyboardException error) {
            return error.kind();
        } catch (IllegalArgumentException error) {
            return "invalid"; // a limits constructor refuses with an IllegalArgumentException
        }
    }

    public static void main(String[] arguments) throws Exception {
        Map<String, Object> file = Fixtures.load("keyboard", 1);
        Set<String> codes = Fixtures.codes();
        Map<String, Object> ceilings = Json.object(file.get("ceilings"));
        KeyboardLimits standard = new KeyboardLimits();
        check(standard.maxSelectionChars == num(ceilings, "maxSelection") && standard.maxInsertChars == num(ceilings, "maxInsert"), "defaults");
        for (Object raw : Fixtures.cases(file, "limits")) {
            Map<String, Object> c = Json.object(raw);
            String got = code(() -> new KeyboardLimits(num(c, "maxSelection"), num(c, "maxInsert")));
            expect("limits/" + c.get("name"), codes, (String) c.get("code"), got);
        }
        for (Object raw : Fixtures.cases(file, "selection")) {
            Map<String, Object> c = Json.object(raw);
            Connection connection = new Connection();
            connection.selection = (String) c.get("value");
            KeyboardEditor editor = new KeyboardEditor(connection, new KeyboardLimits(num(c, "maxSelection"), 1));
            String got = editor.selection();
            check(c.get("want") == null ? got == null : c.get("want").equals(got), "selection/" + c.get("name") + ": got " + got);
        }
        for (Object raw : Fixtures.cases(file, "insert")) {
            Map<String, Object> c = Json.object(raw);
            KeyboardEditor editor = new KeyboardEditor(new Connection(), new KeyboardLimits(1, num(c, "maxInsert")));
            expect("insert/" + c.get("name"), codes, (String) c.get("code"), code(() -> editor.insert((String) c.get("text"))));
        }
        for (Object raw : Fixtures.cases(file, "delete")) {
            Map<String, Object> c = Json.object(raw);
            KeyboardEditor editor = new KeyboardEditor(new Connection(), new KeyboardLimits(1, num(c, "maxInsert")));
            expect("delete/" + c.get("name") + " (backward)", codes, (String) c.get("code"), code(() -> editor.deleteBackward(num(c, "count"))));
            expect("delete/" + c.get("name") + " (forward)", codes, (String) c.get("code"), code(() -> editor.deleteForward(num(c, "count"))));
        }
        for (Object raw : Fixtures.cases(file, "context")) {
            Map<String, Object> c = Json.object(raw);
            Connection connection = new Connection();
            boolean before = "before".equals(c.get("where"));
            if (before) {
                connection.before = (String) c.get("value");
            } else {
                connection.after = (String) c.get("value");
            }
            KeyboardEditor editor = new KeyboardEditor(connection);
            String got = before ? editor.contextBefore(num(c, "limit")) : editor.contextAfter(num(c, "limit"));
            check(c.get("want") == null ? got == null : c.get("want").equals(got), "context/" + c.get("name") + ": got " + got);
        }
        System.out.println(checks + " checks, " + failures + " failures");
        if (failures != 0) {
            System.exit(1);
        }
    }

    private static void expect(String name, Set<String> codes, String want, String got) {
        check(want == null || codes.contains(want), name + ": code " + want + " is not in codes.json");
        check(want == null ? got == null : want.equals(got), name + ": got " + got + ", want " + want);
    }

    private static int num(Map<String, Object> m, String key) {
        return ((Long) m.get(key)).intValue();
    }

    private KeyboardFixtureTests() {}
}
