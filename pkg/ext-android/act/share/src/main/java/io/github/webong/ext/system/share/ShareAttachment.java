package io.github.webong.ext.system.share;

import java.util.Arrays;
import java.util.Objects;

/**
 * One shared file. Exactly one of {@code data} and {@code path} carries the
 * content; {@code byteCount} is the content size and must agree with
 * {@code data} when both are present.
 */
public final class ShareAttachment {
    private final String name;
    private final String mimeType;
    private final long byteCount;
    private final byte[] data;
    private final String path;

    public ShareAttachment(String name, String mimeType, long byteCount, byte[] data, String path) {
        this.name = name;
        this.mimeType = mimeType;
        this.byteCount = byteCount;
        this.data = data == null ? null : Arrays.copyOf(data, data.length);
        this.path = path;
    }

    public String name() { return name; }
    public String mimeType() { return mimeType; }
    public long byteCount() { return byteCount; }
    public byte[] data() { return data == null ? null : Arrays.copyOf(data, data.length); }
    public String path() { return path; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof ShareAttachment)) return false;
        ShareAttachment value = (ShareAttachment) other;
        return Objects.equals(name, value.name) && Objects.equals(mimeType, value.mimeType)
                && byteCount == value.byteCount && Arrays.equals(data, value.data)
                && Objects.equals(path, value.path);
    }

    @Override public int hashCode() {
        return Objects.hash(name, mimeType, byteCount, path) * 31 + Arrays.hashCode(data);
    }
}
