package io.github.webong.ext.system;

import java.util.Objects;

public final class ResourceCollectionStatus {
    private final String state;
    private final String detail;
    private final String observedAt;

    public ResourceCollectionStatus(String state, String detail, String observedAt) {
        if (state == null || state.isEmpty()) throw new IllegalArgumentException("collection state is required");
        this.state = state;
        this.detail = detail;
        this.observedAt = observedAt;
    }

    public String state() { return state; }
    public String detail() { return detail; }
    public String observedAt() { return observedAt; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof ResourceCollectionStatus)) return false;
        ResourceCollectionStatus value = (ResourceCollectionStatus) other;
        return state.equals(value.state) && Objects.equals(detail, value.detail) && Objects.equals(observedAt, value.observedAt);
    }
    @Override public int hashCode() { return Objects.hash(state, detail, observedAt); }
}
