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
        if (platform == null || platform.isEmpty() || !SurfaceWire.timestamp(observedAt) || status == null) {
            throw new IllegalArgumentException("accessibility platform, timestamp, and status are required");
        }
        java.util.Map<String, String> parents = new java.util.HashMap<>();
        for (AccessibilityElement element : elements) {
            String parent = element.parentId() == null || element.parentId().isEmpty() ? null : element.parentId();
            if (parents.containsKey(element.id())) throw new IllegalArgumentException("element ids must be unique");
            parents.put(element.id(), parent);
        }
        for (java.util.Map.Entry<String, String> entry : parents.entrySet()) {
            String parent = entry.getValue();
            for (int steps = 0; parent != null; steps++) {
                if (!parents.containsKey(parent) || parent.equals(entry.getKey()) || steps > parents.size()) {
                    throw new IllegalArgumentException("a parent is another element of the snapshot, without a cycle");
                }
                parent = parents.get(parent);
            }
        }
        this.platform = platform; this.observedAt = observedAt; this.status = status;
        this.elements = Collections.unmodifiableList(new ArrayList<>(elements));
    }

    public String platform() { return platform; }
    public String observedAt() { return observedAt; }
    public SurfaceCollectionStatus status() { return status; }
    public List<AccessibilityElement> elements() { return elements; }
}
