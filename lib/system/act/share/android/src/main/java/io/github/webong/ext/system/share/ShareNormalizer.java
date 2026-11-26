package io.github.webong.ext.system.share;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

/**
 * Validates and merges already-parsed share items. Exact duplicate texts,
 * links, and attachments collapse to their first occurrence so a provider that
 * repeats an item cannot multiply an event. Two different attachments with one
 * file name are rejected rather than renamed.
 */
public final class ShareNormalizer {
    private ShareNormalizer() {}

    public static SharePayload normalize(List<ShareLoadedItem> items, ShareLimits limits)
            throws ShareException {
        if (limits == null) throw new ShareException("invalid", "share limits are required");
        limits.validate();
        if (items == null) items = List.of();
        if (items.size() > limits.maxItems) throw limit("items", items.size(), limits.maxItems);

        List<String> subjects = new ArrayList<>();
        List<String> texts = new ArrayList<>();
        List<String> urls = new ArrayList<>();
        List<ShareAttachment> attachments = new ArrayList<>();
        Set<String> subjectSeen = new HashSet<>();
        Set<String> textSeen = new HashSet<>();
        Set<String> urlSeen = new HashSet<>();
        Set<String> attachmentSeen = new HashSet<>();
        Map<String, String> attachmentNames = new HashMap<>();
        long byteCount = 0;
        for (ShareLoadedItem item : items) {
            if (item == null) throw new ShareException("invalid", "share item is required");
            String subject = text(item.subject(), limits.maxTextBytes, "subject");
            if (subject != null && subjectSeen.add(subject)) subjects.add(subject);
            String text = text(item.text(), limits.maxTextBytes, "text");
            if (text != null && textSeen.add(text)) texts.add(text);
            if (urlSeen.size() + item.urls().size() > limits.maxItems) {
                throw limit("links", urlSeen.size() + item.urls().size(), limits.maxItems);
            }
            for (String raw : item.urls()) {
                String link = link(raw, limits.maxUrlLength);
                if (urlSeen.add(link)) urls.add(link);
            }
            for (ShareAttachment candidate : item.attachments()) {
                ShareAttachment attachment = attachment(candidate, limits);
                String key = key(attachment);
                if (!attachmentSeen.add(key)) continue;
                String folded = attachment.name().toLowerCase(Locale.ROOT);
                String previous = attachmentNames.get(folded);
                if (previous != null && !previous.equals(key)) {
                    throw new ShareException("invalid",
                            "two different attachments share the name " + attachment.name());
                }
                attachmentNames.put(folded, key);
                byteCount += attachment.byteCount();
                if (byteCount > limits.maxBytes) {
                    throw new ShareException("too_large",
                            "attachment bytes exceed the bound of " + limits.maxBytes);
                }
                attachments.add(attachment);
            }
        }
        String mergedSubject = subjects.isEmpty() ? null : String.join("\n", subjects);
        String mergedText = texts.isEmpty() ? null : String.join("\n", texts);
        if (utf8(mergedSubject) > limits.maxTextBytes || utf8(mergedText) > limits.maxTextBytes) {
            throw new ShareException("too_large", "merged text exceeds the configured bound");
        }
        return new SharePayload(mergedSubject, mergedText, urls, attachments, byteCount);
    }

    static String text(String value, int limit, String name) throws ShareException {
        if (value == null) return null;
        if (value.indexOf('\0') >= 0) {
            throw new ShareException("invalid", name + " must not contain NUL");
        }
        int bytes = utf8(value);
        if (bytes > limit) throw limit(name + " bytes", bytes, limit);
        return value.isEmpty() ? null : value;
    }

    /**
     * A link's normal form: trimmed, with its scheme lowercased and nothing else changed. It has no
     * spaces or control characters and starts with a scheme (a letter, then letters, digits, plus,
     * dot or dash, then a colon); parsing it is the embedding application's.
     */
    static String link(String raw, int limit) throws ShareException {
        String value = raw == null ? "" : trim(raw);
        if (value.isEmpty()) throw new ShareException("invalid", "link must not be empty");
        int bytes = utf8(value);
        if (bytes > limit) throw limit("link bytes", bytes, limit);
        for (int index = 0; index < value.length(); index++) {
            char character = value.charAt(index);
            if (character == ' ' || Character.isISOControl(character)) {
                throw new ShareException("invalid", "link must not contain spaces or control characters");
            }
        }
        int colon = value.indexOf(':');
        if (colon < 1 || !validScheme(value, colon)) {
            throw new ShareException("invalid", "link must start with a scheme");
        }
        return value.substring(0, colon).toLowerCase(Locale.ROOT) + value.substring(colon);
    }

    private static boolean validScheme(String value, int end) {
        for (int index = 0; index < end; index++) {
            char character = value.charAt(index);
            boolean letter = character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z';
            boolean digit = character >= '0' && character <= '9';
            if (index == 0 ? !letter : !letter && !digit && character != '+' && character != '-' && character != '.') {
                return false;
            }
        }
        return true;
    }

    /**
     * Strips leading and trailing U+0009 to U+000D and U+0020, the rule of every binding of this
     * contract. Unicode spaces and the other control characters are content.
     */
    static String trim(String value) {
        int start = 0;
        int end = value.length();
        while (start < end && space(value.charAt(start))) start++;
        while (end > start && space(value.charAt(end - 1))) end--;
        return value.substring(start, end);
    }

    private static boolean space(char character) {
        return character == ' ' || character >= '\t' && character <= '\r';
    }

    static ShareAttachment attachment(ShareAttachment value, ShareLimits limits) throws ShareException {
        if (value == null) throw new ShareException("invalid", "attachment is required");
        String name = value.name() == null ? "" : trim(value.name());
        if (!portableName(name)) {
            throw new ShareException("invalid", "attachment name is not a portable file name");
        }
        String mimeType = value.mimeType() == null ? "" : trim(value.mimeType());
        if (mimeType.isEmpty()) mimeType = "application/octet-stream";
        if (mimeType.indexOf('\0') >= 0
                || mimeType.getBytes(StandardCharsets.UTF_8).length > ShareLimits.MAX_MIME_TYPE_LENGTH) {
            throw new ShareException("invalid", "attachment media type is invalid");
        }
        byte[] data = value.data();
        String path = value.path();
        if (data != null && path != null) {
            throw new ShareException("invalid", "attachment carries both data and a path");
        }
        long size = value.byteCount();
        if (data != null) {
            if (size != 0 && size != data.length) {
                throw new ShareException("invalid", "declared size disagrees with attachment data");
            }
            size = data.length;
        }
        if (size < 0) throw new ShareException("invalid", "attachment size must not be negative");
        if (path != null && !path.startsWith("/")) {
            throw new ShareException("invalid", "attachment path must be absolute");
        }
        return new ShareAttachment(name, mimeType, size, data, path);
    }

    public static boolean portableName(String value) {
        if (value == null || value.isEmpty())
            return false;
        if (value.getBytes(StandardCharsets.UTF_8).length > ShareLimits.MAX_NAME_LENGTH)
            return false;
        if (value.equals(".") || value.equals(".."))
            return false;
        if (value.indexOf('/') >= 0 || value.indexOf('\\') >= 0 || value.indexOf('\0') >= 0)
            return false;
        for (int index = 0; index < value.length(); index++) {
            if (Character.isISOControl(value.charAt(index)))
                return false;
        }
        return true;
    }

    static String key(ShareAttachment attachment) {
        String content = attachment.path();
        if (attachment.data() != null) content = sha256(attachment.data());
        return attachment.name().toLowerCase(Locale.ROOT) + "\0" + attachment.mimeType() + "\0"
                + attachment.byteCount() + "\0" + (content == null ? "" : content);
    }

    static String sha256(byte[] data) {
        try {
            return java.util.HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(data));
        } catch (NoSuchAlgorithmException error) {
            throw new IllegalStateException("SHA-256 is unavailable", error);
        }
    }

    static int utf8(String value) {
        return value == null ? 0 : value.getBytes(StandardCharsets.UTF_8).length;
    }

    private static ShareException limit(String field, long actual, long bound) {
        return new ShareException("too_large", field + " exceeded the bound of " + bound + ": " + actual);
    }
}
