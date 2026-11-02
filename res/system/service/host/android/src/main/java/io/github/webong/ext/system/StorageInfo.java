package io.github.webong.ext.system;

import java.util.Objects;

public final class StorageInfo {
    private final String device;
    private final String mountPoint;
    private final long totalBytes;
    private final long availableBytes;
    private final String type;
    private final boolean readOnly;

    public StorageInfo(String device, String mountPoint, long totalBytes, long availableBytes,
                       String type, boolean readOnly) {
        if (device == null || device.isEmpty() || mountPoint == null || mountPoint.isEmpty()) {
            throw new IllegalArgumentException("storage identity and mount point are required");
        }
        this.device = device;
        this.mountPoint = mountPoint;
        this.totalBytes = totalBytes;
        this.availableBytes = availableBytes;
        this.type = type;
        this.readOnly = readOnly;
    }

    public String device() { return device; }
    public String mountPoint() { return mountPoint; }
    public long totalBytes() { return totalBytes; }
    public long availableBytes() { return availableBytes; }
    public String type() { return type; }
    public boolean readOnly() { return readOnly; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof StorageInfo)) return false;
        StorageInfo value = (StorageInfo) other;
        return device.equals(value.device) && mountPoint.equals(value.mountPoint) && totalBytes == value.totalBytes
            && availableBytes == value.availableBytes && Objects.equals(type, value.type) && readOnly == value.readOnly;
    }
    @Override public int hashCode() {
        return Objects.hash(device, mountPoint, totalBytes, availableBytes, type, readOnly);
    }
}
