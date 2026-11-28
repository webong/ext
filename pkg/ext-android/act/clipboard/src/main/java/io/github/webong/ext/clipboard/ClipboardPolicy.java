package io.github.webong.ext.clipboard;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;

public final class ClipboardPolicy {
    public static final int MAX_BYTES = 16 * 1024 * 1024;
    public static final int MAX_ITEMS = 64;
    public static final int MAX_FORMATS = 32;

    public final int maxBytes;
    public final List<String> allowedFormats;
    public final boolean allowRead, allowWrite;

    public ClipboardPolicy(int maxBytes, List<String> allowedFormats, boolean allowRead, boolean allowWrite)
            throws ClipboardException {
        if (allowedFormats == null) {
            throw new ClipboardException("invalid", "clipboard formats are required");
        }
        this.maxBytes = maxBytes;
        this.allowedFormats = java.util.Collections.unmodifiableList(new ArrayList<>(allowedFormats));
        this.allowRead = allowRead;
        this.allowWrite = allowWrite;
        validate();
    }

    public void validate() throws ClipboardException {
        if (maxBytes <= 0 || maxBytes > MAX_BYTES || allowedFormats.isEmpty()
                || allowedFormats.size() > MAX_FORMATS || new HashSet<>(allowedFormats).size() != allowedFormats.size()) {
            throw new ClipboardException("invalid", "clipboard needs a byte ceiling and explicit formats");
        }
        for (String format : allowedFormats) {
            if (format == null || format.isEmpty() || utf8Length(format) > 256) {
                throw new ClipboardException("invalid", "invalid clipboard format");
            }
        }
    }

    static int utf8Length(String value) {
        return value.getBytes(java.nio.charset.StandardCharsets.UTF_8).length;
    }
}
