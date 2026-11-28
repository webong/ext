package io.github.webong.ext.network;

import io.github.webong.ext.system.fixtures.Fixtures;
import io.github.webong.ext.system.fixtures.Json;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Runs testdata/network/v1 and testdata/capability/v1, which the Go and Swift bindings run as well. */
public final class NetworkFixtureTests {
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
            return "invalid"; // the model's constructors refuse with an IllegalArgumentException
        }
    }

    public static void main(String[] arguments) throws Exception {
        Map<String, Object> file = Fixtures.load("network", 1);
        Set<String> codes = Fixtures.codes();
        Map<String, Object> defaults = Json.object(file.get("defaults"));
        NetworkOptions standard = new NetworkOptions();
        check(standard.maxInterfaces() == num(defaults, "maxInterfaces") && standard.maxAddresses() == num(defaults, "maxAddresses")
                && standard.maxRoutes() == num(defaults, "maxRoutes"), "defaults");
        for (Object raw : Fixtures.cases(file, "options")) {
            Map<String, Object> c = Json.object(raw);
            expect("options/" + c.get("name"), codes, (String) c.get("code"),
                    code(() -> new NetworkOptions(num(c, "maxInterfaces"), num(c, "maxAddresses"), num(c, "maxRoutes"))));
        }
        for (Object raw : Fixtures.cases(file, "addresses")) {
            Map<String, Object> c = Json.object(raw);
            String name = "addresses/" + c.get("name");
            NetworkAddress[] got = new NetworkAddress[1];
            String code = code(() -> got[0] = NetworkAddress.of((String) c.get("ip"), num(c, "prefix")));
            expect(name, codes, (String) c.get("code"), code);
            if (c.get("code") == null && got[0] != null) {
                Map<String, Object> want = Json.object(c.get("want"));
                check(got[0].equals(new NetworkAddress((String) want.get("ip"), num(want, "prefixLength"), (String) want.get("family"), (String) want.get("scope"))),
                        name + ": got " + got[0].ip() + "/" + got[0].prefixLength() + " " + got[0].family() + " " + got[0].scope());
            }
        }
        for (Object raw : Fixtures.cases(file, "resolver")) {
            Map<String, Object> c = Json.object(raw);
            Map<String, Object> want = Json.object(c.get("want"));
            for (String list : new String[] {"servers", "search", "options"}) {
                check(NetworkResolver.clean(strings(c.get(list))).equals(strings(want.get(list))), "resolver/" + c.get("name") + ": " + list);
            }
        }
        Map<String, Object> capability = Fixtures.load("capability", 1);
        for (Object raw : Fixtures.cases(capability, "cases")) {
            Map<String, Object> c = Json.object(raw);
            String reason = (String) c.get("reason");
            expect("capability/" + c.get("state") + (reason == null ? "" : "+" + reason), codes, (String) c.get("code"),
                    code(() -> new NetworkCollectionStatus((String) c.get("state"), reason, null, null)));
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

    private static List<String> strings(Object value) {
        List<String> out = new ArrayList<>();
        for (Object s : Json.array(value)) {
            out.add((String) s);
        }
        return out;
    }

    private NetworkFixtureTests() {}
}
