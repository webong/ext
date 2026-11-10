package io.github.webong.ext.system.share;

import io.github.webong.ext.system.fixtures.Fixtures;
import io.github.webong.ext.system.fixtures.Json;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Runs testdata/share/v1, which the Go and Swift bindings run as well. */
public final class ShareFixtureTests {
    private static int checks;
    private static int failures;

    private static void check(boolean value, String name) {
        checks++;
        if (!value) {
            failures++;
            System.out.println("FAIL " + name);
        }
    }

    public static void main(String[] arguments) throws Exception {
        Map<String, Object> file = Fixtures.load("share", 1);
        Set<String> codes = Fixtures.codes();
        Map<String, Object> defaults = Json.object(file.get("defaults"));
        ShareLimits standard = new ShareLimits();
        check(standard.maxItems == num(defaults, "maxItems") && standard.maxBytes == lng(defaults, "maxBytes")
                && standard.maxTextBytes == num(defaults, "maxTextBytes") && standard.maxUrlLength == num(defaults, "maxUrlLength"),
                "defaults");
        for (Object raw : Fixtures.cases(file, "limits")) {
            Map<String, Object> c = Json.object(raw);
            Map<String, Object> l = Json.object(c.get("limits"));
            String got = null;
            try {
                new ShareLimits(num(l, "maxItems"), lng(l, "maxBytes"), num(l, "maxTextBytes"), num(l, "maxUrlLength"));
            } catch (ShareException error) {
                got = error.kind();
            }
            expect("limits/" + c.get("name"), codes, (String) c.get("code"), got);
        }
        for (Object raw : Fixtures.cases(file, "normalize")) {
            Map<String, Object> c = Json.object(raw);
            String name = "normalize/" + c.get("name");
            ShareLimits limits = limits(defaults, c.get("limits"));
            List<ShareLoadedItem> items = new ArrayList<>();
            for (Object item : Json.array(c.get("items"))) {
                items.add(item(Json.object(item)));
            }
            String got = null;
            SharePayload payload = null;
            try {
                payload = ShareNormalizer.normalize(items, limits);
            } catch (ShareException error) {
                got = error.kind();
            }
            expect(name, codes, (String) c.get("code"), got);
            if (c.get("code") == null && payload != null) {
                compare(name, Json.object(c.get("payload")), payload);
            }
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

    private static long lng(Map<String, Object> m, String key) {
        return (Long) m.get(key);
    }

    private static ShareLimits limits(Map<String, Object> defaults, Object spec) throws ShareException {
        Map<String, Object> l = spec == null ? java.util.Collections.<String, Object>emptyMap() : Json.object(spec);
        return new ShareLimits(
                num(l.containsKey("maxItems") ? l : defaults, "maxItems"),
                lng(l.containsKey("maxBytes") ? l : defaults, "maxBytes"),
                num(l.containsKey("maxTextBytes") ? l : defaults, "maxTextBytes"),
                num(l.containsKey("maxUrlLength") ? l : defaults, "maxUrlLength"));
    }

    private static ShareLoadedItem item(Map<String, Object> spec) {
        List<String> urls = new ArrayList<>();
        if (spec.containsKey("urls")) {
            for (Object u : Json.array(spec.get("urls"))) {
                urls.add((String) u);
            }
        }
        List<ShareAttachment> attachments = new ArrayList<>();
        if (spec.containsKey("attachments")) {
            for (Object a : Json.array(spec.get("attachments"))) {
                attachments.add(attachment(Json.object(a)));
            }
        }
        return new ShareLoadedItem((String) spec.get("subject"), (String) spec.get("text"), urls, attachments);
    }

    private static byte[] data(Map<String, Object> spec) {
        if (spec.containsKey("text")) {
            return ((String) spec.get("text")).getBytes(StandardCharsets.UTF_8);
        }
        return spec.containsKey("zeros") ? new byte[((Long) spec.get("zeros")).intValue()] : null;
    }

    private static ShareAttachment attachment(Map<String, Object> spec) {
        return new ShareAttachment((String) spec.get("name"), (String) spec.get("mimeType"),
                spec.containsKey("size") ? (Long) spec.get("size") : 0L, data(spec), (String) spec.get("path"));
    }

    private static void compare(String name, Map<String, Object> want, SharePayload got) {
        check(want.get("subject").equals(got.subject() == null ? "" : got.subject()), name + ": subject");
        check(want.get("text").equals(got.text() == null ? "" : got.text()), name + ": text");
        check(want.get("byteCount").equals(got.byteCount()), name + ": byteCount");
        List<Object> urls = Json.array(want.get("urls"));
        check(urls.equals(got.urls()), name + ": urls " + got.urls());
        List<Object> attachments = Json.array(want.get("attachments"));
        check(attachments.size() == got.attachments().size(), name + ": attachment count");
        for (int i = 0; i < attachments.size() && i < got.attachments().size(); i++) {
            Map<String, Object> w = Json.object(attachments.get(i));
            ShareAttachment g = got.attachments().get(i);
            check(w.get("name").equals(g.name()) && w.get("mimeType").equals(g.mimeType())
                    && w.get("size").equals(g.byteCount())
                    && Arrays.equals(data(w), g.data())
                    && (w.get("path") == null ? g.path() == null : w.get("path").equals(g.path())), name + ": attachment " + i);
        }
    }

    private ShareFixtureTests() {}
}
