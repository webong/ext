package io.github.webong.ext.clipboard;

import io.github.webong.ext.system.fixtures.Fixtures;
import io.github.webong.ext.system.fixtures.Json;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Set;

public final class ClipboardTests {
    private static int checks;
    private static void check(boolean value, String name) { checks++; if (!value) throw new AssertionError(name); }

    public static void main(String[] args) throws Exception {
        ClipboardPolicy policy = new ClipboardPolicy(5, Collections.singletonList("text/plain"), true, true);
        check(policy.maxBytes == 5, "policy");
        ClipboardItem item = new ClipboardItem(Collections.singletonList(
                new ClipboardRepresentation("text/plain", "hello".getBytes(StandardCharsets.UTF_8))));
        ClipboardValidation.validateItems(Collections.singletonList(item), 5, policy.allowedFormats);
        try { ClipboardValidation.validateItems(Collections.singletonList(item), 4, policy.allowedFormats); check(false, "limit rejected"); }
        catch (ClipboardException expected) { check(expected.code.equals("too_large"), "limit code"); }
        try { new ClipboardPolicy(5, Arrays.asList("text/plain", "text/plain"), true, true); check(false, "duplicate rejected"); }
        catch (ClipboardException expected) { check(expected.code.equals("invalid"), "duplicate code"); }
        byte[] input = new byte[] {1};
        ClipboardRepresentation copy = new ClipboardRepresentation("text/plain", input);
        input[0] = 2;
        check(copy.data[0] == 1, "defensive data copy");
        fixtures();
        System.out.println(checks + " checks, 0 failures");
    }

    /** testdata/clipboard/v1, which the Go, Swift and C# bindings run as well. */
    private static void fixtures() throws Exception {
        Map<String, Object> file = Fixtures.load("clipboard", 1);
        Set<String> codes = Fixtures.codes();
        for (Object raw : Fixtures.cases(file, "policy")) {
            Map<String, Object> c = Json.object(raw);
            String name = "policy/" + c.get("name");
            String got = null;
            try {
                new ClipboardPolicy(((Long) c.get("maxBytes")).intValue(), strings(c.get("formats")), true, true);
            } catch (ClipboardException error) {
                got = error.code;
            }
            expect(name, codes, (String) c.get("code"), got);
        }
        for (Object raw : Fixtures.cases(file, "items")) {
            Map<String, Object> c = Json.object(raw);
            String name = "items/" + c.get("name");
            List<ClipboardItem> items = new ArrayList<>();
            for (Object item : Json.array(c.get("items"))) {
                items.addAll(build(Json.object(item)));
            }
            String got = null;
            try {
                ClipboardValidation.validateItems(items, ((Long) c.get("maxBytes")).intValue(), strings(c.get("formats")));
            } catch (ClipboardException error) {
                got = error.code;
            }
            expect(name, codes, (String) c.get("code"), got);
        }
    }

    private static void expect(String name, Set<String> codes, String want, String got) {
        check(want == null || codes.contains(want), name + ": code " + want + " is not in codes.json");
        check(want == null ? got == null : want.equals(got), name + ": got " + got + ", want " + want);
    }

    private static List<String> strings(Object value) {
        List<String> out = new ArrayList<>();
        for (Object s : Json.array(value)) {
            out.add((String) s);
        }
        return out;
    }

    private static List<ClipboardItem> build(Map<String, Object> spec) {
        long times = spec.containsKey("times") ? (Long) spec.get("times") : 1;
        List<ClipboardRepresentation> representations = new ArrayList<>();
        for (Object raw : Json.array(spec.get("representations"))) {
            Map<String, Object> r = Json.object(raw);
            byte[] data = r.containsKey("text")
                    ? ((String) r.get("text")).getBytes(StandardCharsets.UTF_8)
                    : new byte[r.containsKey("zeros") ? ((Long) r.get("zeros")).intValue() : 0];
            representations.add(new ClipboardRepresentation((String) r.get("format"), data));
        }
        List<ClipboardItem> out = new ArrayList<>();
        for (long n = 0; n < times; n++) {
            out.add(new ClipboardItem(representations));
        }
        return out;
    }
}
