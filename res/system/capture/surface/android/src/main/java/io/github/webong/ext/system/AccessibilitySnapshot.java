package io.github.webong.ext.system;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class AccessibilitySnapshot {
    private final String platform;
    private final String observedAt;
    private final SurfaceCollectionStatus status;
    private final List<AccessibilityElement> elements;

    public AccessibilitySnapshot(String platform, String observedAt, SurfaceCollectionStatus status,
            List<AccessibilityElement> elements) {
        if (platform == null || platform.isEmpty() || observedAt == null || observedAt.isEmpty() || status == null) {
            throw new IllegalArgumentException("accessibility platform, timestamp, and status are required");
        }
        this.platform = platform; this.observedAt = observedAt; this.status = status;
        this.elements = Collections.unmodifiableList(new ArrayList<>(elements));
    }

    public String platform() { return platform; }
    public String observedAt() { return observedAt; }
    public SurfaceCollectionStatus status() { return status; }
    public List<AccessibilityElement> elements() { return elements; }
}
