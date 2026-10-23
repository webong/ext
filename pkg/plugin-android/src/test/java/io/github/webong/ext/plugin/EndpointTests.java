package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.HashSet;
import java.util.Set;

/** Data endpoints, run from GuestTests. The fixture is the one every binding reproduces. */
final class EndpointTests {
    private EndpointTests() {}

    interface Call {
        void run() throws GuestException;
    }

    static String outcome(Call call) {
        try {
            call.run();
            return "ok";
        } catch (GuestException e) {
            switch (e.status()) {
                case Endpoint.INVALID:
                    return "invalid";
                case Endpoint.DENIED:
                    return "denied";
                case Endpoint.EXPIRED:
                    return "expired";
                default:
                    return "status " + e.status();
            }
        }
    }

    static byte[] bytes(String text) {
        return text.getBytes(StandardCharsets.UTF_8);
    }

    /** The end of the JSON value that starts at {@code start}. */
    static int valueEnd(String s, int start) {
        int depth = 0;
        boolean quoted = false;
        int i = start;
        for (; i < s.length(); i++) {
            char c = s.charAt(i);
            if (quoted) {
                if (c == '\\') {
                    i++;
                } else if (c == '"') {
                    quoted = false;
                    if (depth == 0) return i + 1;
                }
            } else if (c == '"') {
                quoted = true;
            } else if (c == '{' || c == '[') {
                depth++;
            } else if (c == '}' || c == ']') {
                if (depth == 0) return i;
                depth--;
                if (depth == 0) return i + 1;
            } else if (c == ',' && depth == 0) {
                return i;
            }
        }
        return i;
    }

    /** The raw JSON text of a top-level member of the object {@code s}. The fixture has duplicate keys, which a parser would collapse. */
    static String member(String s, String key) {
        int i = s.indexOf('{') + 1;
        while (i < s.length()) {
            while (i < s.length() && (s.charAt(i) == ' ' || s.charAt(i) == ',')) i++;
            if (i >= s.length() || s.charAt(i) != '"') break;
            int nameEnd = s.indexOf('"', i + 1);
            String name = s.substring(i + 1, nameEnd);
            i = nameEnd + 1;
            while (i < s.length() && (s.charAt(i) == ' ' || s.charAt(i) == ':')) i++;
            int stop = valueEnd(s, i);
            if (name.equals(key)) return s.substring(i, stop);
            i = stop;
        }
        return "";
    }

    static String unquote(String s) {
        return s.replace("\"", "");
    }

    static void fixture() throws Exception {
        // Run from the repository root, as scripts/plugin-android.sh does.
        Path path = Path.of(System.getProperty("ext.endpoint.cases", "lib/plugin/testdata/v1/endpoint-cases.json"));
        if (!Files.exists(path)) {
            System.out.println("skip endpoint fixture: " + path + " not found");
            return;
        }
        int total = 0;
        Set<String> seen = new HashSet<>();
        for (String raw : Files.readAllLines(path, StandardCharsets.UTF_8)) {
            String line = raw.strip();
            if (line.endsWith(",")) line = line.substring(0, line.length() - 1);
            if (!line.startsWith("{\"name\"")) continue;
            String name = member(line, "name");
            byte[] endpoint = bytes(member(line, "endpoint"));
            byte[] policy = bytes(member(line, "policy"));
            String wantCheck = unquote(member(line, "check"));
            String wantValidate = unquote(member(line, "validate"));
            expect(wantCheck, outcome(() -> Endpoint.check(endpoint)), "check: " + name);
            expect(wantValidate, outcome(() -> Endpoint.validate(endpoint, policy)), "validate: " + name);
            seen.add(wantValidate);
            total++;
        }
        if (total != 213) throw new AssertionError("fixture has " + total + " cases, want 213");
        if (seen.size() != 4) throw new AssertionError("fixture states only " + seen);
    }

    static void inputs() throws Exception {
        byte[] endpoint = bytes("{\"scheme\":\"ws\",\"address\":\"localhost:80\",\"expiresAt\":\"2030-01-01T00:30:00Z\"}");
        expect("ok", outcome(() -> Endpoint.check(endpoint)), "complete endpoint");
        expect("invalid", outcome(() -> Endpoint.check(bytes("[]"))), "not an object");
        expect("invalid", outcome(() -> Endpoint.check(new byte[0])), "empty");
        // A policy that names no scheme admits nothing.
        expect("denied", outcome(() -> Endpoint.validate(endpoint, bytes("{\"now\":\"2030-01-01T00:00:00Z\"}"))), "no scheme");
        expect("ok", outcome(() -> Endpoint.validate(endpoint, bytes("{\"schemes\":[\"ws\"],\"now\":\"2030-01-01T00:00:00Z\"}"))), "admitted");
        expect("expired", outcome(() -> Endpoint.validate(endpoint, bytes("{\"schemes\":[\"ws\"],\"now\":\"2030-01-01T01:00:00Z\"}"))), "expired");
        // The error carries a status and nothing of the endpoint.
        byte[] secret = bytes("{\"scheme\":\"ws\",\"address\":\"localhost:80\",\"token\":\"s3cr3t-t0ken\",\"expiresAt\":\"2000-01-01T00:00:00Z\"}");
        try {
            Endpoint.validate(secret, bytes("{\"schemes\":[\"ws\"],\"now\":\"2030-01-01T00:00:00Z\"}"));
            throw new AssertionError("an expired endpoint was admitted");
        } catch (GuestException e) {
            if (e.status() != Endpoint.EXPIRED || String.valueOf(e.getMessage()).contains("s3cr3t")) {
                throw new AssertionError("status " + e.status() + " message " + e.getMessage());
            }
        }
    }

    static void expect(String want, String got, String what) {
        if (!want.equals(got)) throw new AssertionError(what + ": want " + want + ", got " + got);
    }
}
