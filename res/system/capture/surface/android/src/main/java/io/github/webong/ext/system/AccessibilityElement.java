package io.github.webong.ext.system;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

public final class AccessibilityElement {
    private final String id;
    private final String parentId;
    private final int pid;
    private final String processStartId;
    private final String role;
    private final String identifier;
    private final String label;
    private final String value;
    private final SurfaceBounds bounds;
    private final List<String> actions;
    private final Boolean enabled;
    private final Boolean focused;

    public AccessibilityElement(String id, String parentId, int pid, String processStartId,
            String role, String identifier, String label, String value, SurfaceBounds bounds,
            List<String> actions, Boolean enabled, Boolean focused) {
        if (id == null || id.isEmpty() || pid <= 0 || processStartId == null || processStartId.isEmpty()
                || role == null || role.isEmpty()) {
            throw new IllegalArgumentException("accessibility identity, process generation, and role are required");
        }
        this.id = id; this.parentId = parentId; this.pid = pid; this.processStartId = processStartId;
        this.role = role; this.identifier = identifier; this.label = label; this.value = value;
        this.bounds = bounds; this.actions = Collections.unmodifiableList(new ArrayList<>(actions));
        this.enabled = enabled; this.focused = focused;
    }

    public String id() { return id; }
    public String parentId() { return parentId; }
    public int pid() { return pid; }
    public String processStartId() { return processStartId; }
    public String role() { return role; }
    public String identifier() { return identifier; }
    public String label() { return label; }
    public String value() { return value; }
    public SurfaceBounds bounds() { return bounds; }
    public List<String> actions() { return actions; }
    public Boolean enabled() { return enabled; }
    public Boolean focused() { return focused; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof AccessibilityElement)) return false;
        AccessibilityElement item = (AccessibilityElement) other;
        return id.equals(item.id) && Objects.equals(parentId, item.parentId) && pid == item.pid
                && processStartId.equals(item.processStartId) && role.equals(item.role)
                && Objects.equals(identifier, item.identifier) && Objects.equals(label, item.label)
                && Objects.equals(value, item.value) && Objects.equals(bounds, item.bounds)
                && actions.equals(item.actions) && Objects.equals(enabled, item.enabled)
                && Objects.equals(focused, item.focused);
    }

    @Override public int hashCode() {
        return Objects.hash(id, parentId, pid, processStartId, role, identifier, label, value, bounds, actions, enabled, focused);
    }
}
