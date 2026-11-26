package io.github.webong.ext.system;

import java.util.Arrays;
import java.util.List;
import java.util.Objects;

public final class ResourceCollectionStatus {
    private final String state;
    private final String reason;
    private final String detail;
    private final String observedAt;

    public ResourceCollectionStatus(String state, String detail, String observedAt) {
        this(state, null, detail, observedAt);
    }

    /**
     * A state (complete, partial, permission-denied, unsupported or unavailable) and, where something
     * is missing, why: unsupported, permission, no_device or tool_missing (testdata/capability/v1).
     *
     * @throws IllegalArgumentException when the state is unknown or the reason does not fit it
     */
    public ResourceCollectionStatus(String state, String reason, String detail, String observedAt) {
        if (state == null || state.isEmpty()) throw new IllegalArgumentException("collection state is required");
        List<String> allowed;
        switch (state) {
            case "complete":
            case "partial":
                allowed = Arrays.asList((String) null);
                break;
            case "permission-denied":
                allowed = Arrays.asList(null, "permission");
                break;
            case "unsupported":
                allowed = Arrays.asList("unsupported", "tool_missing");
                break;
            case "unavailable":
                allowed = Arrays.asList(null, "permission", "no_device", "tool_missing");
                break;
            default:
                throw new IllegalArgumentException("collection state is unknown");
        }
        if (!allowed.contains(reason)) throw new IllegalArgumentException("collection reason does not fit its state");
        this.state = state;
        this.reason = reason;
        this.detail = detail;
        this.observedAt = observedAt;
    }

    public String state() { return state; }
    /** Why something is missing, or null. */
    public String reason() { return reason; }
    public String detail() { return detail; }
    public String observedAt() { return observedAt; }
    @Override public boolean equals(Object other) {
        if (!(other instanceof ResourceCollectionStatus)) return false;
        ResourceCollectionStatus value = (ResourceCollectionStatus) other;
        return state.equals(value.state) && Objects.equals(reason, value.reason) && Objects.equals(detail, value.detail)
            && Objects.equals(observedAt, value.observedAt);
    }
    @Override public int hashCode() { return Objects.hash(state, reason, detail, observedAt); }
}
