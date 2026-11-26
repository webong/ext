package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;

/**
 * Archive install through the Java binding, against every case of the shared fixtures
 * (lib/plugin/testdata/v1/archive-cases.json), which the Go stores, the C engine and the other
 * bindings run too. Run from GuestTests.
 */
final class ArchiveTests {
    private ArchiveTests() {}

    static Path fixtures() {
        return Path.of(System.getProperty("ext.fixtures", "lib/plugin/testdata/v1"));
    }

    static int statusOf(String name) {
        return switch (name == null ? "ok" : name) {
            case "ok" -> 0;
            case "invalid" -> PackageStore.INVALID;
            case "mismatch" -> PackageStore.MISMATCH;
            case "unsupported" -> PackageStore.UNSUPPORTED;
            case "not_found" -> PackageStore.NOT_FOUND;
            case "exists" -> PackageStore.EXISTS;
            default -> throw new AssertionError("unknown status " + name);
        };
    }

    static long number(Map<String, Object> object, String key) {
        Object value = object == null ? null : object.get(key);
        return value instanceof Json.Num n ? Long.parseLong(n.text()) : 0;
    }

    static String revision(byte[] manifest) {
        Map<String, Object> identity = Json.object(Json.object(Json.parse(manifest)).get("identity"));
        return Json.string(identity.get("revision"));
    }

    /** Every case of the shared fixtures, step by step. */
    static void cases() throws Exception {
        Path dir = fixtures();
        Map<String, Object> fixture = Json.object(Json.parse(Files.readAllBytes(dir.resolve("archive-cases.json"))));
        List<?> cases = (List<?>) fixture.get("cases");
        StoreTests.require(cases.size() >= 71, "the fixtures hold " + cases.size() + " cases");
        Path escape = Path.of("/tmp/ext-archive-escape");
        int failures = 0;
        for (Object item : cases) {
            Map<String, Object> c = Json.object(item);
            String name = Json.string(c.get("name"));
            try {
                runCase(dir, c);
                if (Files.exists(escape)) throw new AssertionError("an entry escaped to " + escape);
            } catch (AssertionError e) {
                failures++;
                System.out.println("  archive case " + name + ": " + e.getMessage());
            }
        }
        if (failures > 0) throw new AssertionError(failures + " archive cases failed");
    }

    static void runCase(Path dir, Map<String, Object> c) throws Exception {
        Map<String, Object> l = Json.object(c.get("limits"));
        StoreLimits limits = new StoreLimits(number(l, "maxArtifactBytes"), (int) number(l, "maxTreeFiles"),
                number(l, "maxTreeBytes"), number(l, "maxArchiveBytes"));
        try (StoreTests.Scratch scratch = new StoreTests.Scratch()) {
            Path root = scratch.path("plugins");
            try (PackageStore store = new PackageStore(root.toString(), limits)) {
                int n = 0;
                for (Object s : (List<?>) c.get("steps")) {
                    Map<String, Object> step = Json.object(s);
                    String where = "step " + n++;
                    int want = statusOf(Json.string(step.get("status")));
                    if (step.containsKey("install")) {
                        String archive = dir.resolve(Json.string(step.get("install"))).toString();
                        String sha = Json.string(step.get("sha256"));
                        String id = Json.string(step.get("id"));
                        byte[][] got = new byte[1][];
                        StoreTests.fails(want, () -> got[0] = store.installArchive(archive, sha, id, false), where + " install");
                        if (want == 0) {
                            StoreTests.same(Json.string(step.get("revision")), revision(got[0]), where + " revision");
                            StoreTests.same(revision(got[0]), revision(store.load(idOf(got[0]))),
                                    where + " installed revision");
                        }
                    } else if (step.containsKey("receipt")) {
                        String id = Json.string(step.get("receipt"));
                        byte[][] got = new byte[1][];
                        StoreTests.fails(want, () -> got[0] = store.receipt(id), where + " receipt");
                        if (want == 0) {
                            Map<String, Object> receipt = Json.object(Json.parse(got[0]));
                            StoreTests.same(id, Json.string(receipt.get("id")), where + " receipt id");
                            StoreTests.same(Json.string(step.get("revision")), Json.string(receipt.get("revision")), where + " receipt revision");
                            StoreTests.same(Json.string(step.get("sha256")), Json.string(receipt.get("artifact_sha256")), where + " receipt digest");
                        }
                    } else if (step.containsKey("remove")) {
                        String id = Json.string(step.get("remove"));
                        StoreTests.fails(want, () -> store.remove(id), where + " remove");
                    } else if (step.containsKey("damage")) {
                        Path file = root.resolve(Json.string(step.get("damage")).replace("/", "%2f"))
                                .resolve(Json.string(step.get("path")));
                        Files.write(file, "damaged".getBytes(StandardCharsets.US_ASCII));
                    } else {
                        throw new AssertionError(where + ": unknown step " + step.keySet());
                    }
                }
            }
            // Nothing is left but package directories and the receipts.
            if (Files.exists(root)) {
                try (Stream<Path> entries = Files.list(root)) {
                    for (Path entry : entries.toList()) {
                        String name = entry.getFileName().toString();
                        StoreTests.require(name.equals(".receipts") || (!name.startsWith(".") && Files.isDirectory(entry)
                                && Files.exists(entry.resolve("plugin.json"))), "left in the store: " + name);
                    }
                }
            }
        }
    }

    static String idOf(byte[] manifest) {
        Map<String, Object> identity = Json.object(Json.object(Json.parse(manifest)).get("identity"));
        return Json.string(identity.get("id"));
    }

    /** What the fixtures do not show: arguments, a URL, a missing archive and a closed store. */
    static void arguments() throws Exception {
        String zeros = "0".repeat(64);
        try (StoreTests.Scratch scratch = new StoreTests.Scratch()) {
            PackageStore store = new PackageStore(scratch.string("plugins"));
            StoreTests.fails(PackageStore.MISSING, () -> store.installArchive(scratch.string("absent"), zeros), "a missing archive");
            for (String bad : new String[] {"", "https://example.invalid/a.zip", "file:///tmp/a.zip", "a\0b"}) {
                StoreTests.fails(PackageStore.INVALID, () -> store.installArchive(bad, zeros), "archive " + bad);
            }
            Path archive = fixtures().resolve("archives/tar-gz-top.bin");
            StoreTests.fails(PackageStore.INVALID, () -> store.installArchive(archive.toString(), "abc"), "a short digest");
            StoreTests.fails(PackageStore.INVALID, () -> store.installArchive(archive.toString(), zeros, "a\0b", false), "a NUL in the id");
            StoreTests.fails(PackageStore.NOT_FOUND, () -> store.receipt("example/none"), "the receipt of nothing");
            store.close();
            StoreTests.fails(PackageStore.CLOSED, () -> store.installArchive(archive.toString(), zeros), "a closed store");
            StoreTests.fails(PackageStore.CLOSED, () -> store.receipt("example/archive"), "a receipt from a closed store");
        }
        try {
            new StoreLimits(0, 0, 0, -1);
            throw new AssertionError("a negative archive limit was accepted");
        } catch (IllegalArgumentException expected) {
            // refused before the engine sees it
        }
    }
}
