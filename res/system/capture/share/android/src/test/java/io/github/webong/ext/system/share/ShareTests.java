package io.github.webong.ext.system.share;

import java.nio.charset.StandardCharsets;
import java.util.List;

public final class ShareTests {
    public static void main(String[] arguments) throws Exception {
        defaults();
        duplicatesAndMerging();
        conflictingNames();
        bounds();
        invalidValues();
    }

    private static void defaults() throws Exception {
        ShareLimits limits = new ShareLimits();
        assert limits.maxItems == 32;
        assert limits.maxBytes == 16L * 1024 * 1024;
        assert limits.maxTextBytes == 64 * 1024;
    }

    private static void duplicatesAndMerging() throws Exception {
        ShareAttachment attachment = new ShareAttachment(
                "report.txt", "text/plain", 0, "hello".getBytes(StandardCharsets.UTF_8), null);
        SharePayload payload = ShareNormalizer.normalize(List.of(
                new ShareLoadedItem("Notes", "first", List.of("https://example.test/a"), List.of()),
                new ShareLoadedItem("Notes", "first",
                        List.of("https://example.test/a", " HTTPS://example.test/b "), List.of()),
                new ShareLoadedItem(null, "second", List.of(), List.of(attachment, attachment))),
                new ShareLimits());
        assert "Notes".equals(payload.subject());
        assert "first\nsecond".equals(payload.text());
        assert payload.urls().equals(List.of("https://example.test/a", "https://example.test/b"));
        assert payload.attachments().size() == 1;
        assert payload.attachments().get(0).byteCount() == 5;
        assert payload.byteCount() == 5;
    }

    private static void conflictingNames() throws Exception {
        boolean rejected = false;
        try {
            ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, null, List.of(), List.of(
                    new ShareAttachment("photo.jpg", "image/jpeg", 0,
                            "one".getBytes(StandardCharsets.UTF_8), null),
                    new ShareAttachment("Photo.jpg", "image/jpeg", 0,
                            "two".getBytes(StandardCharsets.UTF_8), null)))), new ShareLimits());
        } catch (ShareException expected) {
            rejected = "invalid".equals(expected.kind());
        }
        assert rejected;
    }

    private static void bounds() throws Exception {
        byte[] large = new byte[64];
        expectLimit(() -> ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, null, List.of(),
                List.of(new ShareAttachment("large.bin", "application/octet-stream", 0, large, null)))),
                new ShareLimits(32, 32, 64 * 1024, 8192)));
        expectLimit(() -> ShareNormalizer.normalize(List.of(
                new ShareLoadedItem(null, "0123456789", List.of(), List.of()),
                new ShareLoadedItem(null, "abcdefghij", List.of(), List.of())),
                new ShareLimits(32, 1024, 12, 8192)));
        expectLimit(() -> ShareNormalizer.normalize(List.of(
                new ShareLoadedItem(null, "a", List.of(), List.of()),
                new ShareLoadedItem(null, "b", List.of(), List.of()),
                new ShareLoadedItem(null, "c", List.of(), List.of())),
                new ShareLimits(2, 1024, 1024, 8192)));
        expectLimit(() -> ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, null,
                List.of(), List.of(new ShareAttachment("stream.bin", "application/octet-stream", 1024, null,
                        "/tmp/stream.bin")))), new ShareLimits(32, 512, 1024, 8192)));
    }

    private static void invalidValues() throws Exception {
        for (String name : new String[] {"../escape", "nested/file", "", "..", "bad\u0000name"}) {
            expectInvalid(() -> ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, null,
                    List.of(), List.of(new ShareAttachment(name, "text/plain", 1, null, null)))),
                    new ShareLimits()));
        }
        expectInvalid(() -> ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, null,
                List.of("not a url"), List.of())), new ShareLimits()));
        expectInvalid(() -> ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, "value\u0000",
                List.of(), List.of())), new ShareLimits()));
        expectInvalid(() -> ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, null,
                List.of(), List.of(new ShareAttachment("value", "text/plain", 1, new byte[] {1}, "/tmp/value")))),
                new ShareLimits()));
        expectInvalid(() -> ShareNormalizer.normalize(List.of(new ShareLoadedItem(null, null,
                List.of(), List.of(new ShareAttachment("value", "text/plain", 1, null, "relative")))),
                new ShareLimits()));
        boolean rejected = false;
        try { new ShareLimits(ShareLimits.MAX_ITEMS + 1, 1, 1, 1); }
        catch (ShareException expected) { rejected = true; }
        assert rejected;
        assert ShareNormalizer.normalize(List.of(), new ShareLimits()).attachments().isEmpty();
    }

    private interface Check { void run() throws Exception; }

    private static void expectLimit(Check check) throws Exception {
        boolean limited = false;
        try { check.run(); }
        catch (ShareException expected) { limited = "limit".equals(expected.kind()); }
        assert limited;
    }

    private static void expectInvalid(Check check) throws Exception {
        boolean invalid = false;
        try { check.run(); }
        catch (ShareException expected) { invalid = "invalid".equals(expected.kind()); }
        assert invalid;
    }

    private ShareTests() {}
}
