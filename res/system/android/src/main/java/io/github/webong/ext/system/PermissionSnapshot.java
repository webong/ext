package io.github.webong.ext.system;

public final class PermissionSnapshot {
    private final String platform;
    private final String observedAt;
    private final PermissionInfo accessibility;
    private final PermissionInfo screenRecording;

    public PermissionSnapshot(String platform, String observedAt, PermissionInfo accessibility,
            PermissionInfo screenRecording) {
        if (platform == null || platform.isEmpty() || observedAt == null || observedAt.isEmpty()
                || accessibility == null || screenRecording == null) {
            throw new IllegalArgumentException("permission snapshot fields are required");
        }
        this.platform = platform; this.observedAt = observedAt;
        this.accessibility = accessibility; this.screenRecording = screenRecording;
    }

    public String platform() { return platform; }
    public String observedAt() { return observedAt; }
    public PermissionInfo accessibility() { return accessibility; }
    public PermissionInfo screenRecording() { return screenRecording; }
}
