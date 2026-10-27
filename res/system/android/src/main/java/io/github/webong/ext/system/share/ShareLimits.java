package io.github.webong.ext.system.share;

/**
 * Bounds fixed by the embedding application before a share intent is read.
 * Every field has a library ceiling so a caller cannot disable bounding.
 */
public final class ShareLimits {
    public static final int MAX_ITEMS = 128;
    public static final long MAX_BYTES = 64L * 1024 * 1024;
    public static final int MAX_TEXT_BYTES = 1024 * 1024;
    public static final int MAX_URL_LENGTH = 8192;
    public static final int MAX_NAME_LENGTH = 255;
    public static final int MAX_MIME_TYPE_LENGTH = 255;

    public final int maxItems;
    public final long maxBytes;
    public final int maxTextBytes;
    public final int maxUrlLength;

    public ShareLimits() throws ShareException {
        this(32, 16L * 1024 * 1024, 64 * 1024, 8192);
    }

    public ShareLimits(int maxItems, long maxBytes, int maxTextBytes, int maxUrlLength) throws ShareException {
        this.maxItems = maxItems;
        this.maxBytes = maxBytes;
        this.maxTextBytes = maxTextBytes;
        this.maxUrlLength = maxUrlLength;
        validate();
    }

    public void validate() throws ShareException {
        if (maxItems < 1 || maxItems > MAX_ITEMS
                || maxBytes < 1 || maxBytes > MAX_BYTES
                || maxTextBytes < 1 || maxTextBytes > MAX_TEXT_BYTES
                || maxUrlLength < 1 || maxUrlLength > MAX_URL_LENGTH) {
            throw new ShareException("invalid", "share limits must be positive and within library ceilings");
        }
    }
}
