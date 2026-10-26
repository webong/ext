package io.github.webong.ext.network;

public final class NetworkCollectionStatus {
    private final String state;
    private final String detail;
    private final String observedAt;

    public NetworkCollectionStatus(String state, String detail, String observedAt) {
        if (state == null || state.isEmpty()) throw new IllegalArgumentException("network collection state is required");
        this.state = state; this.detail = detail; this.observedAt = observedAt;
    }

    public String state() { return state; }
    public String detail() { return detail; }
    public String observedAt() { return observedAt; }
}
