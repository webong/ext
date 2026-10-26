package io.github.webong.ext.web;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;

/** Identical artifact checks on the JVM and an Android device, using public APIs only. */
public final class ExtensionArtifactChecks {
    private ExtensionArtifactChecks() {}
    public interface Linker { void create(File link, File target) throws Exception; }
    public interface Check { void accept(boolean ok, String name); }
    private interface Operation { void run() throws Exception; }
    private static void rejects(Check check, String name, Operation operation) throws Exception {
        try { operation.run(); check.accept(false, name); }
        catch (IOException expected) { check.accept(true, name); }
    }
    private static String text(File file) throws IOException {
        try (InputStream in = new FileInputStream(file); ByteArrayOutputStream out = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[8192]; int n;
            while ((n = in.read(buffer)) != -1) out.write(buffer, 0, n);
            return new String(out.toByteArray(), StandardCharsets.UTF_8);
        }
    }
    public static void run(File fixtures, File parent, Linker links, Check check) throws Exception {
        File root = new File(parent, "extension-" + UUID.randomUUID());
        if (!root.mkdirs()) throw new IOException("test directory");
        try {
            Map<String,Object> expected = Json.object(Json.parse(text(new File(fixtures, "expected.json"))), "expected");
            String revision = Json.string(expected, "revision");
            for (Object value : (List<?>) Json.parse(text(new File(fixtures, "cases.json")))) {
                Map<String,Object> c = Json.object(value, "case");
                File input = new File(fixtures, Json.string(c, "file"));
                if (Boolean.TRUE.equals(c.get("valid"))) {
                    ExtensionArtifacts.Description d = ExtensionArtifacts.inspect(input);
                    check.accept(d.revision.equals(revision) && d.files == 4 && d.bytes == 191 &&
                        d.name.equals("Example") && d.version.equals("1.0") && d.manifestVersion == 3 &&
                        d.id.equals(Json.string(expected, "id")) && d.permissions.equals(Arrays.asList("storage", "tabs")) &&
                        d.hostPermissions.equals(Collections.singletonList("https://example.test/*")), input.getName());
                } else rejects(check, input.getName(), () -> ExtensionArtifacts.inspect(input));
            }
            File source = new File(root, "source"), a = new File(root, "a.zip"), b = new File(root, "b.zip");
            check.accept(ExtensionArtifacts.stageWithRevision(new File(fixtures, "wrapped.zip"), source, revision).revision.equals(revision), "stage reviewed source");
            check.accept(ExtensionArtifacts.checkedSource(source, revision).revision.equals(revision), "check reviewed source");
            rejects(check, "requires reviewed revision", () -> ExtensionArtifacts.checkedSource(source, ""));
            check.accept(ExtensionArtifacts.packageArchive(source, a).revision.equals(revision), "package revision");
            ExtensionArtifacts.packageArchive(source, b);
            check.accept(ExtensionArtifacts.fileSHA256(a).equals(ExtensionArtifacts.fileSHA256(b)), "deterministic archive");
            check.accept(ExtensionArtifacts.inspect(a).revision.equals(revision), "package roundtrip");
            check.accept(ExtensionArtifacts.fileSHA256(a).equals(ExtensionArtifacts.fileSHA256(new File(fixtures, "mobile.zip"))), "Go-readable canonical mobile ZIP");
            rejects(check, "exclusive package", () -> ExtensionArtifacts.packageArchive(source, a));
            rejects(check, "exclusive stage", () -> ExtensionArtifacts.stage(a, source));
            rejects(check, "outside source", () -> ExtensionArtifacts.packageArchive(source, new File(source, "inside.zip")));
            try (FileOutputStream out = new FileOutputStream(new File(source, "scripts/main.js"))) { out.write(1); }
            rejects(check, "changed revision", () -> ExtensionArtifacts.checkedSource(source, revision));
            File stale = new File(root, "stale");
            rejects(check, "changed stage", () -> ExtensionArtifacts.stageWithRevision(source, stale, revision));
            check.accept(!stale.exists(), "stale stage not created");
            File alias = new File(root, "alias");
            links.create(alias, source);
            rejects(check, "source symlink", () -> ExtensionArtifacts.inspect(alias));
            rejects(check, "output through alias", () -> ExtensionArtifacts.packageArchive(source, new File(alias, "inside.zip")));
            rejects(check, "output through missing alias descendants", () -> ExtensionArtifacts.packageArchive(source, new File(alias, "new/deep/inside.zip")));
            // Remove aliases before the recursive test cleanup.
            alias.delete();
            File dangling = new File(root, "dangling.zip");
            links.create(dangling, new File(root, "absent"));
            rejects(check, "dangling output", () -> ExtensionArtifacts.packageArchive(source, dangling));
            dangling.delete();
            File link = new File(source, "link");
            links.create(link, a);
            rejects(check, "contained symlink", () -> ExtensionArtifacts.inspect(source));
            link.delete();
            File broken = new File(root, "broken");
            rejects(check, "failed stage", () -> ExtensionArtifacts.stage(new File(fixtures, "long-name.zip"), broken));
            check.accept(!broken.exists(), "failed stage cleaned up");
            File copy = new File(root, "copy.zip");
            ExtensionArtifacts.copyArtifactExclusive(a, copy);
            check.accept(ExtensionArtifacts.fileSHA256(a).equals(ExtensionArtifacts.fileSHA256(copy)), "artifact copy hash");
            rejects(check, "exclusive copy", () -> ExtensionArtifacts.copyArtifactExclusive(b, copy));
            File unused = new File(root, "new/output.ZIP");
            ExtensionArtifacts.unusedArtifact(unused, ".zip");
            check.accept(unused.getParentFile().isDirectory(), "unused output parent");
            rejects(check, "used artifact", () -> ExtensionArtifacts.unusedArtifact(a, ".zip"));
            rejects(check, "artifact suffix", () -> ExtensionArtifacts.unusedArtifact(new File(root, "bad.txt"), ".zip"));
            try (RandomAccessFile file = new RandomAccessFile(new File(source, "large"), "rw")) { file.setLength(17 << 20); }
            rejects(check, "oversized directory entry", () -> ExtensionArtifacts.inspect(source));
        } finally { remove(root); }
    }
    private static void remove(File file) {
        File[] children = file.listFiles();
        if (children != null) for (File child : children) remove(child);
        file.delete();
    }
}
