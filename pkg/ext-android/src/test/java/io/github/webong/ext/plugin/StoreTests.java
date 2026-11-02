package io.github.webong.ext.plugin;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermission;
import java.nio.file.attribute.PosixFilePermissions;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicReference;
import java.util.stream.Stream;

/**
 * The package store of the C engine through the Java binding: the lifecycle, the failures each
 * call has, permissions, limits and the manifest copied as it was, then the reports and the
 * layered view. The Go package store is the baseline; pkg/ext-go/cgoengine holds the C store to
 * it, and these check that the binding reaches the same behavior (the Rust, Node and Zig
 * bindings run the same scenarios). Run from GuestTests.
 */
final class StoreTests {
    private StoreTests() {}

    interface Action {
        void run() throws Exception;
    }

    /** The status an action fails with, or 0 when it succeeds. */
    static int status(Action action) throws Exception {
        try {
            action.run();
            return 0;
        } catch (GuestException e) {
            return e.status();
        }
    }

    static void fails(int want, Action action, String what) throws Exception {
        int got = status(action);
        if (got != want) throw new AssertionError(what + ": want status " + want + ", got " + got);
    }

    static void require(boolean ok, String message) {
        if (!ok) throw new AssertionError(message);
    }

    static void same(byte[] want, byte[] got, String what) {
        if (!Arrays.equals(want, got)) {
            throw new AssertionError(what + ": want " + new String(want, StandardCharsets.UTF_8)
                    + ", got " + new String(got, StandardCharsets.UTF_8));
        }
    }

    static void same(Object want, Object got, String what) {
        if (!want.equals(got)) throw new AssertionError(what + ": want " + want + ", got " + got);
    }

    static void sameMode(int want, int got, String what) {
        if (want != got) throw new AssertionError(what + ": want " + Integer.toOctalString(want) + ", got " + Integer.toOctalString(got));
    }

    static String text(byte[] bytes) {
        return new String(bytes, StandardCharsets.UTF_8);
    }

    /** A scratch directory removed when it goes out of scope. */
    static final class Scratch implements AutoCloseable {
        final Path dir;

        Scratch() throws IOException {
            dir = Files.createTempDirectory("ext-java-store-");
        }

        Path path(String name) {
            return dir.resolve(name);
        }

        String string(String name) {
            return path(name).toString();
        }

        @Override
        public void close() throws IOException {
            remove(dir);
        }
    }

    static void remove(Path path) throws IOException {
        if (!Files.exists(path)) return;
        try (Stream<Path> walk = Files.walk(path)) {
            for (Path p : walk.sorted(Comparator.reverseOrder()).toList()) Files.delete(p);
        }
    }

    static void write(Path dir, String rel, String body, boolean executable) throws IOException {
        Path path = dir.resolve(rel);
        Files.createDirectories(path.getParent());
        Files.write(path, body.getBytes(StandardCharsets.UTF_8));
        Files.setPosixFilePermissions(path, PosixFilePermissions.fromString(executable ? "rwx------" : "rw-------"));
    }

    static String sha256(byte[] bytes) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    }

    /**
     * The directory digest of a tree artifact: the SHA-256 of "./path sha256\n" for every file in
     * path order (ext_directory_digest). The engine checks it, so a mistake here shows as MISMATCH.
     */
    static String treeDigest(Path dir) throws Exception {
        List<Path> files;
        try (Stream<Path> walk = Files.walk(dir)) {
            files = walk.filter(Files::isRegularFile).toList();
        }
        List<String> names = new ArrayList<>();
        for (Path file : files) names.add(dir.relativize(file).toString().replace('\\', '/'));
        names.sort(null);
        StringBuilder lines = new StringBuilder();
        for (String name : names) {
            lines.append("./").append(name).append(' ').append(sha256(Files.readAllBytes(dir.resolve(name)))).append('\n');
        }
        return sha256(lines.toString().getBytes(StandardCharsets.UTF_8));
    }

    /**
     * A source package at {@code dir}: {@code src/main.py} holding {@code body}, and with {@code tree}
     * the tree artifact {@code data}. {@code contract} is what an upgrade may not change. The
     * manifest is spelled with tabs and a trailing blank line, so a store that re-encoded it would
     * not return the bytes the source had.
     */
    static String pack(Path dir, String id, String revision, String contract, String body, boolean tree) throws Exception {
        remove(dir);
        write(dir, "src/main.py", body, false);
        StringBuilder artifacts = new StringBuilder("{\"name\":\"main\",\"path\":\"src/main.py\",\"sha256\":\""
                + sha256(body.getBytes(StandardCharsets.UTF_8)) + "\"}");
        if (tree) {
            write(dir, "data/x.txt", "ex", false);
            write(dir, "data/sub/y.txt", "why", false);
            write(dir, "data/run.sh", "#!/bin/sh\n", true);
            artifacts.append(",\n\t\t{\"name\":\"data\",\"path\":\"data\",\"sha256\":\"")
                    .append(treeDigest(dir.resolve("data"))).append("\",\"kind\":\"tree\"}");
        }
        String manifest = "{\"apiVersion\":\"ext.package/v1\",\n\t\"descriptor\":{\"apiVersion\":\"ext.plugin/v1\","
                + "\"identity\":{\"id\":\"" + id + "\",\"revision\":\"" + revision + "\"},"
                + "\"contracts\":[{\"name\":\"" + contract + "\",\"version\":\"v1\",\"operations\":[{\"name\":\"read\"}]}]},\n"
                + "\t\"artifacts\":[" + artifacts + "]}\r\n\r\n";
        write(dir, "plugin.json", manifest, false);
        return dir.toString();
    }

    static List<String> entries(Path root) throws IOException {
        if (!Files.isDirectory(root)) return List.of();
        try (Stream<Path> list = Files.list(root)) {
            return list.map(p -> p.getFileName().toString()).sorted().toList();
        }
    }

    static int mode(Path path) throws IOException {
        int bits = 0;
        for (PosixFilePermission p : Files.getPosixFilePermissions(path)) bits |= 1 << (8 - p.ordinal());
        return bits;
    }

    static String read(Path path) throws IOException {
        return Files.readString(path);
    }

    static void tamper(Path file) throws IOException {
        Files.writeString(file, "tampered");
    }

    static void emptyAndMissing() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path root = scratch.path("store");
            try (PackageStore store = new PackageStore(root.toString())) {
                same(List.of(), store.ids(), "ids of an empty store");
                fails(PackageStore.NOT_FOUND, () -> store.load("example/a"), "load");
                fails(PackageStore.NOT_FOUND, () -> store.remove("example/a"), "remove");
                String source = pack(scratch.path("s"), "example/a", "r1", "example.read", "one", false);
                fails(PackageStore.NOT_FOUND, () -> store.upgrade(source), "upgrade of what is not installed");
                fails(PackageStore.INVALID, () -> store.load("../bad"), "an id that is not valid");
                fails(PackageStore.INVALID, () -> store.remove("Bad"), "an id that is not valid");
                fails(PackageStore.INVALID, () -> store.install(""), "an empty source");
                // what has no spelling as a path is invalid, not another path
                fails(PackageStore.INVALID, () -> store.load("example/a\0b"), "a NUL in an id");
                fails(PackageStore.INVALID, () -> store.install(source + "\0x"), "a NUL in a path");
                fails(PackageStore.INVALID, () -> store.load("example/\ud800"), "a lone surrogate");
                require(!Files.exists(root), "a failed call made the root");
                // a report of a missing root is empty, and asking does not make it
                same("[]", text(store.list()), "list");
                same("[]", text(store.verify()), "verify");
                same("{\"packages\":[],\"problems\":[]}", text(store.listUnverified()), "listUnverified");
                require(!Files.exists(root), "a report made the root");
            }
            // an existing, empty root is the same store
            Files.createDirectory(scratch.path("empty"));
            try (PackageStore store = new PackageStore(scratch.string("empty"))) {
                same(List.of(), store.ids(), "ids of an empty root");
                same("[]", text(store.list()), "list of an empty root");
            }
            fails(PackageStore.INVALID, () -> new PackageStore("").close(), "an empty root");
            fails(PackageStore.INVALID, () -> new PackageStore("a\0b").close(), "a NUL in a root");
            try {
                new PackageStore(null);
                throw new AssertionError("a null root was accepted");
            } catch (NullPointerException expected) {
                // the argument is a programming error, not a store failure
            }
        }
    }

    static void statuses() {
        // The values are the engine's ext_status (lib/plugin/include/ext_host.h).
        same(1, PackageStore.INVALID, "INVALID");
        same(3, PackageStore.MISMATCH, "MISMATCH");
        same(5, PackageStore.CLOSED, "CLOSED");
        same(7, PackageStore.IO, "IO");
        same(8, PackageStore.NOMEM, "NOMEM");
        same(10, PackageStore.NOT_FOUND, "NOT_FOUND");
        same(18, PackageStore.EXISTS, "EXISTS");
        same(19, PackageStore.MISSING, "MISSING");
    }

    static void lifecycle() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path root = scratch.path("store");
            try (PackageStore store = new PackageStore(root.toString())) {
                // install returns plugin.json as the source had it, and never replaces
                String first = pack(scratch.path("s1"), "example/a", "r1", "example.read", "one", true);
                byte[] source = Files.readAllBytes(scratch.path("s1/plugin.json"));
                same(source, store.install(first), "install returns the manifest");
                same(source, Files.readAllBytes(root.resolve("example%2fa/plugin.json")), "the installed manifest");
                fails(PackageStore.EXISTS, () -> store.install(first), "install over an installed id");
                same(List.of("example/a"), store.ids(), "ids");
                same(source, store.load("example/a"), "load");
                sameMode(0700, mode(root.resolve("example%2fa")), "package directory");
                sameMode(0600, mode(root.resolve("example%2fa/src/main.py")), "file artifact");
                sameMode(0600, mode(root.resolve("example%2fa/data/x.txt")), "file in a tree");
                sameMode(0700, mode(root.resolve("example%2fa/data/run.sh")), "executable file in a tree");

                // upgrade: a new revision of the same id and contracts, in place
                String second = pack(scratch.path("s2"), "example/a", "r2", "example.read", "two", true);
                byte[] renewed = Files.readAllBytes(scratch.path("s2/plugin.json"));
                same(renewed, store.upgrade(second), "upgrade returns the manifest");
                same("two", read(root.resolve("example%2fa/src/main.py")), "upgraded content");
                same(List.of("example%2fa"), entries(root), "staging or backup left behind");
                fails(PackageStore.INVALID, () -> store.upgrade(second), "same revision");
                String other = pack(scratch.path("s3"), "example/a", "r3", "example.write", "three", false);
                fails(PackageStore.MISMATCH, () -> store.upgrade(other), "different contracts");
                same("two", read(root.resolve("example%2fa/src/main.py")), "the failed upgrade changed the package");

                // replace may change the contracts, and installs what is not there
                store.replace(other);
                same("three", read(root.resolve("example%2fa/src/main.py")), "replaced content");
                require(!Files.exists(root.resolve("example%2fa/data")), "the tree is gone");
                String bee = pack(scratch.path("s4"), "example/b", "r1", "example.read", "bee", false);
                store.replace(bee);
                same(List.of("example/a", "example/b"), store.ids(), "ids after replace");

                store.remove("example/b");
                fails(PackageStore.NOT_FOUND, () -> store.remove("example/b"), "remove again");
                fails(PackageStore.NOT_FOUND, () -> store.load("example/b"), "load a removed package");
                store.remove("example/a");
                same(List.of(), entries(root), "the root after removing everything");
            }
        }
    }

    static void defects() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path root = scratch.path("store");
            try (PackageStore store = new PackageStore(root.toString())) {
                Path dir = scratch.path("s");
                String source = pack(dir, "example/c", "r1", "example.read", "cee", true);
                write(dir, "extra.txt", "unlisted", false);
                fails(PackageStore.INVALID, () -> store.install(source), "an unlisted file");
                Files.delete(dir.resolve("extra.txt"));
                Files.writeString(dir.resolve("src/main.py"), "changed");
                fails(PackageStore.MISMATCH, () -> store.install(source), "a changed artifact");
                Files.delete(dir.resolve("src/main.py"));
                fails(PackageStore.MISSING, () -> store.install(source), "an artifact that is not there");
                write(dir, "src/main.py", "cee", false);
                Files.delete(dir.resolve("data/sub/y.txt"));
                fails(PackageStore.MISMATCH, () -> store.install(source), "a changed tree");
                fails(PackageStore.MISSING, () -> store.install(scratch.string("nowhere")), "a source that is not there");
                Files.delete(dir.resolve("plugin.json"));
                fails(PackageStore.MISSING, () -> store.install(source), "no manifest");
                write(scratch.dir, "file", "x", false);
                fails(PackageStore.INVALID, () -> store.install(scratch.string("file")), "a source that is not a directory");
                // nothing was left below the root, whatever it was
                same(List.of(), entries(root), "the root after failed installs");
            }
        }
    }

    static void names() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path root = scratch.path("store");
            try (PackageStore store = new PackageStore(root.toString())) {
                String source = pack(scratch.path("s"), "example/b", "r1", "example.read", "b", false);
                store.install(source);
                Files.move(root.resolve("example%2fb"), root.resolve("example%2fz"));
                fails(PackageStore.MISMATCH, () -> store.load("example/z"), "a directory under another name");
                fails(PackageStore.MISMATCH, () -> store.remove("example/z"), "remove under another name");
                Files.move(root.resolve("example%2fz"), root.resolve("example%2fb"));
                // changed behind the store's back
                Files.delete(root.resolve("example%2fb/src/main.py"));
                fails(PackageStore.MISSING, () -> store.load("example/b"), "an artifact removed");
                tamper(root.resolve("example%2fb/src/main.py"));
                fails(PackageStore.MISMATCH, () -> store.load("example/b"), "an artifact changed");
                // staging directories are skipped, anything else that is not a package is not
                Files.createDirectory(root.resolve(".install-left"));
                same(List.of("example/b"), store.ids(), "ids with a staging directory");
                write(root, "stray", "x", false);
                fails(PackageStore.INVALID, () -> store.ids(), "a stray file");
                Files.delete(root.resolve("stray"));
                Files.createDirectory(root.resolve("Not-A-Package"));
                fails(PackageStore.INVALID, () -> store.ids(), "a directory that is not a package");
            }
        }
    }

    static void limits() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path root = scratch.path("store");
            String big = pack(scratch.path("big"), "example/f", "r1", "example.read", "three bytes", false);
            try (PackageStore store = new PackageStore(root.toString(), new StoreLimits(2, 0, 0))) {
                fails(PackageStore.INVALID, () -> store.install(big), "an artifact over the limit");
            }
            String tree = pack(scratch.path("tree"), "example/g", "r1", "example.read", "ok", true);
            try (PackageStore store = new PackageStore(root.toString(), new StoreLimits(0, 2, 0))) {
                fails(PackageStore.INVALID, () -> store.install(tree), "a tree with too many files");
            }
            try (PackageStore store = new PackageStore(root.toString(), new StoreLimits(0, 0, 4))) {
                fails(PackageStore.INVALID, () -> store.install(tree), "a tree over the byte limit");
            }
            same(List.of(), entries(root), "the root after refused installs");
            try (PackageStore store = new PackageStore(root.toString(), StoreLimits.DEFAULT)) {
                store.install(tree);
                same(List.of("example/g"), store.ids(), "the default limits admit it");
            }
            try {
                new StoreLimits(-1, 0, 0);
                throw new AssertionError("a negative limit was accepted");
            } catch (IllegalArgumentException expected) {
                // the bounds are non-negative
            }
        }
    }

    static void preserveExecutable() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path root = scratch.path("store");
            try (PackageStore store = new PackageStore(root.toString())) {
                Path dir = scratch.path("s");
                String source = pack(dir, "example/e", "r1", "example.read", "run me", false);
                write(dir, "src/main.py", "run me", true); // the same bytes, executable
                store.install(source);
                sameMode(0600, mode(root.resolve("example%2fe/src/main.py")), "executable bit dropped by default");
                store.replace(source, true);
                sameMode(0700, mode(root.resolve("example%2fe/src/main.py")), "executable bit preserved");
                store.upgrade(pack(scratch.path("s2"), "example/e", "r2", "example.read", "run me", false), true);
                sameMode(0600, mode(root.resolve("example%2fe/src/main.py")), "nothing to preserve in the new source");
            }
        }
    }

    /** A root with a supplementary character in it: the engine must get UTF-8, not modified UTF-8. */
    static void utf8Paths() throws Exception {
        // A desktop JDK names files with the platform's encoding, which is not UTF-8 everywhere, and
        // could neither make nor read such a name there. ART has no such property and names them in UTF-8.
        String encoding = System.getProperty("sun.jnu.encoding");
        if (encoding != null && !"UTF-8".equalsIgnoreCase(encoding)) {
            System.out.println("skip: sun.jnu.encoding is " + encoding);
            return;
        }
        try (Scratch scratch = new Scratch()) {
            String name = "store-😀";
            Path root = scratch.path(name);
            String source = pack(scratch.path("s"), "example/u", "r1", "example.read", "u", false);
            try (PackageStore store = new PackageStore(root.toString())) {
                store.install(source);
                same(List.of("example/u"), store.ids(), "ids");
            }
            try (PackageStore store = new PackageStore(root.toString())) {
                require(store.load("example/u").length > 0, "the store reopened");
            }
            // modified UTF-8 would have named this root with two surrogates of three bytes each
            require(entries(scratch.dir).contains(name), "the root has its UTF-8 name: " + entries(scratch.dir));
            try (LayeredPackageStore view = new LayeredPackageStore(List.of(root.toString()))) {
                same(0, view.load("example/u").layer(), "the layered view reads it too");
            }
        }
    }

    static void threads() throws Exception {
        try (Scratch scratch = new Scratch()) {
            try (PackageStore store = new PackageStore(scratch.string("store"))) {
                Thread[] installers = new Thread[4];
                AtomicReference<Throwable> failure = new AtomicReference<>();
                for (int i = 0; i < installers.length; i++) {
                    String id = "example/t" + i;
                    String source = pack(scratch.path("s" + i), id, "r1", "example.read", id, false);
                    installers[i] = new Thread(() -> {
                        try {
                            store.install(source);
                        } catch (Throwable t) {
                            failure.compareAndSet(null, t);
                        }
                    });
                }
                for (Thread t : installers) t.start();
                for (Thread t : installers) t.join();
                require(failure.get() == null, "an install failed: " + failure.get());
                same(List.of("example/t0", "example/t1", "example/t2", "example/t3"), store.ids(), "ids");
            }

            // close waits for the calls in flight; every later call is CLOSED, and nothing else
            PackageStore store = new PackageStore(scratch.string("store"));
            CountDownLatch busy = new CountDownLatch(4);
            AtomicReference<Throwable> failure = new AtomicReference<>();
            Thread[] readers = new Thread[4];
            for (int i = 0; i < readers.length; i++) {
                readers[i] = new Thread(() -> {
                    boolean counted = false;
                    try {
                        while (true) {
                            store.ids();
                            store.load("example/t0");
                            if (!counted) {
                                counted = true;
                                busy.countDown();
                            }
                        }
                    } catch (GuestException e) {
                        if (e.status() != PackageStore.CLOSED) failure.compareAndSet(null, e);
                    } catch (Throwable t) {
                        failure.compareAndSet(null, t);
                    }
                });
                readers[i].start();
            }
            busy.await();
            store.close();
            for (Thread t : readers) t.join();
            require(failure.get() == null, "a reader failed with something but CLOSED: " + failure.get());
            fails(PackageStore.CLOSED, store::ids, "ids after close");
            fails(PackageStore.CLOSED, () -> store.install(scratch.string("s0")), "install after close");
            fails(PackageStore.CLOSED, store::list, "list after close");
            store.close(); // idempotent
        }
    }

    /** A path as the engine's JSON writes it. */
    static String quoted(Path path) {
        return "\"" + path.toString().replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    /** The part of a report line that names an entry. */
    static String entry(String id, Path directory, int layer) {
        return "\"id\":\"" + id + "\",\"directory\":" + quoted(directory) + ",\"layer\":" + layer;
    }

    static String line(String id, Path directory, int layer, int status) {
        return entry(id, directory, layer) + ",\"status\":" + status;
    }

    static byte[] install(Path root, Scratch scratch, String name, String id, String body) throws Exception {
        String source = pack(scratch.path(name), id, "r1", "example.read", body, false);
        try (PackageStore store = new PackageStore(root.toString())) {
            return store.install(source);
        }
    }

    static void reports() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path root = scratch.path("store");
            try (PackageStore store = new PackageStore(root.toString())) {
                byte[] a = install(root, scratch, "s1", "example/a", "one");
                byte[] b = install(root, scratch, "s2", "example/b", "two");
                Path aDir = root.resolve("example%2fa");
                Path bDir = root.resolve("example%2fb");
                String manifestA = "\"manifest\":" + text(a);
                String manifestB = "\"manifest\":" + text(b);

                String listed = text(store.list());
                require(listed.startsWith("[{" + entry("example/a", aDir, 0)), "list starts with a: " + listed);
                require(listed.contains(entry("example/b", bDir, 0)), "list has b: " + listed);
                require(listed.contains(manifestA) && listed.contains(manifestB), "list embeds the manifests: " + listed);
                same("[{" + line("example/a", aDir, 0, 0) + "},{" + line("example/b", bDir, 0, 0) + "}]",
                        text(store.verify()), "verify");

                // verify reports a bad entry and goes on; list fails closed
                tamper(bDir.resolve("src/main.py"));
                String checks = text(store.verify());
                require(checks.contains(line("example/a", aDir, 0, 0)), "a still verifies: " + checks);
                require(checks.contains(line("example/b", bDir, 0, PackageStore.MISMATCH)), "b does not: " + checks);
                fails(PackageStore.MISMATCH, store::list, "list with a changed artifact");
                // the unverified listing reads no artifact, so it still has both
                String unverified = text(store.listUnverified());
                require(unverified.contains(entry("example/a", aDir, 0)), "a is unverified-listed: " + unverified);
                require(unverified.contains(entry("example/b", bDir, 0)), "b is unverified-listed: " + unverified);
                require(unverified.endsWith("\"problems\":[]}"), "no problems: " + unverified);

                // an entry that is not a package is a problem there, an error in list, and a status in
                // verify; staging directories are skipped
                write(root, "stray", "x", false);
                Files.createDirectory(root.resolve(".install-left"));
                fails(PackageStore.INVALID, store::list, "list with a stray file");
                unverified = text(store.listUnverified());
                require(unverified.contains("\"id\":\"stray\"") && !unverified.contains("install-left"),
                        "stray is a problem, staging is skipped: " + unverified);
                require(unverified.contains(entry("example/a", aDir, 0)), "a is still listed: " + unverified);
                require(text(store.verify()).contains(line("stray", root.resolve("stray"), 0, PackageStore.INVALID)),
                        "stray is INVALID in verify");
            }
        }
    }

    static void layered() throws Exception {
        try (Scratch scratch = new Scratch()) {
            Path high = scratch.path("user");
            Path low = scratch.path("system");
            Path absent = scratch.path("absent");
            byte[] kept = install(high, scratch, "s1", "example/a", "high");
            install(low, scratch, "s2", "example/a", "low");
            byte[] onlyLow = install(low, scratch, "s3", "example/c", "cee");
            try (LayeredPackageStore view = new LayeredPackageStore(
                    List.of(high.toString(), low.toString(), absent.toString()))) {
                LayeredPackageStore.Loaded a = view.load("example/a");
                same(kept, a.manifest(), "the highest root wins");
                same(0, a.layer(), "its layer");
                LayeredPackageStore.Loaded c = view.load("example/c");
                same(onlyLow, c.manifest(), "an id only a lower root holds");
                same(1, c.layer(), "its layer");
                fails(PackageStore.NOT_FOUND, () -> view.load("example/none"), "an id no root holds");
                fails(PackageStore.INVALID, () -> view.load("../bad"), "an id that is not valid");
                String listed = text(view.list());
                require(listed.contains(entry("example/a", high.resolve("example%2fa"), 0)), "a from the high root: " + listed);
                require(listed.contains(entry("example/c", low.resolve("example%2fc"), 1)), "c from the low root: " + listed);
                // the manifest repeats the id, so count the report lines, not the id
                same(1, listed.split("\"id\":\"example/a\",\"directory\"", -1).length - 1, "a shadowed copy is listed");
                require(!Files.exists(absent), "the view made a root");

                // a shadowed copy is never read
                tamper(low.resolve("example%2fa/src/main.py"));
                same(kept, view.load("example/a").manifest(), "load with a shadowed copy damaged");
                view.list();

                // a damaged winner fails closed instead of falling back to the copy below
                tamper(high.resolve("example%2fa/src/main.py"));
                fails(PackageStore.MISMATCH, () -> view.load("example/a"), "a damaged winner");
                fails(PackageStore.MISMATCH, view::list, "list with a damaged winner");
                require(text(view.verify()).contains(line("example/a", high.resolve("example%2fa"), 0, PackageStore.MISMATCH)),
                        "verify names the winner");
                String unverified = text(view.listUnverified());
                require(unverified.contains(entry("example/c", low.resolve("example%2fc"), 1)), "c is listed: " + unverified);
                require(unverified.endsWith("\"problems\":[]}"), "no problems: " + unverified);

                // the view and a store are separate handles on the same roots
                try (PackageStore store = new PackageStore(high.toString())) {
                    store.replace(scratch.string("s1"));
                }
                same(kept, view.load("example/a").manifest(), "the view sees the repaired winner");
            }
            // a lower root with junk in it does not hide a package from a higher one
            write(low, "stray", "x", false);
            try (LayeredPackageStore view = new LayeredPackageStore(List.of(high.toString(), low.toString()))) {
                same(kept, view.load("example/a").manifest(), "load with junk below");
                fails(PackageStore.INVALID, view::list, "list with junk below");
                require(text(view.listUnverified()).contains("\"id\":\"stray\""), "junk is a problem");
            }
            // a view is closed once; later calls say so
            LayeredPackageStore view = new LayeredPackageStore(List.of(high.toString()));
            view.close();
            view.close();
            fails(PackageStore.CLOSED, () -> view.load("example/a"), "load after close");
            fails(PackageStore.CLOSED, view::list, "list after close");
        }
    }

    static void layeredRefusals() throws Exception {
        try (Scratch scratch = new Scratch()) {
            String one = scratch.string("one");
            fails(PackageStore.INVALID, () -> new LayeredPackageStore(List.of()).close(), "no roots");
            fails(PackageStore.INVALID, () -> new LayeredPackageStore(List.of("")).close(), "an empty root");
            fails(PackageStore.INVALID, () -> new LayeredPackageStore(List.of(one, one)).close(), "a repeated root");
            fails(PackageStore.INVALID, () -> new LayeredPackageStore(List.of(one, one + "/./")).close(),
                    "the same root spelled differently");
            fails(PackageStore.INVALID, () -> new LayeredPackageStore(List.of(one, "a\0b")).close(), "a NUL in a root");
            new LayeredPackageStore(List.of(one, scratch.string("two"))).close();
            try {
                new LayeredPackageStore(java.util.Arrays.asList(one, null));
                throw new AssertionError("a null root was accepted");
            } catch (NullPointerException expected) {
                // the argument is a programming error, not a store failure
            }
        }
    }
}
