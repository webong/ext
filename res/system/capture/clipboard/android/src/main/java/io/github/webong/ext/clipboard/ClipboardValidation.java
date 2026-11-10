package io.github.webong.ext.clipboard;

import java.util.HashSet;
import java.util.List;
import java.util.Set;

public final class ClipboardValidation {
    private ClipboardValidation() {}

    public static void validateItems(List<ClipboardItem> items, int maxBytes, List<String> formats)
            throws ClipboardException {
        if (maxBytes <= 0 || maxBytes > ClipboardPolicy.MAX_BYTES) {
            throw new ClipboardException("invalid", "invalid clipboard byte ceiling");
        }
        if (items.size() > ClipboardPolicy.MAX_ITEMS) {
            throw new ClipboardException("too_large", "clipboard item limit exceeded");
        }
        Set<String> allowed = new HashSet<>(formats);
        long bytes = 0;
        for (ClipboardItem item : items) {
            if (item.representations.isEmpty() || item.representations.size() > ClipboardPolicy.MAX_FORMATS) {
                throw new ClipboardException("invalid", "invalid clipboard item");
            }
            Set<String> seen = new HashSet<>();
            for (ClipboardRepresentation representation : item.representations) {
                if (!allowed.contains(representation.format) || !seen.add(representation.format)) {
                    throw new ClipboardException("denied", "clipboard format is not allowed or is repeated");
                }
                bytes += representation.data.length;
                if (bytes > maxBytes) throw new ClipboardException("too_large", "clipboard byte limit exceeded");
            }
        }
    }
}
