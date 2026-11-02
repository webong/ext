package io.github.webong.ext.system;

import java.util.List;
import java.util.Objects;

public final class ContainerInfo {
    private final boolean detected;
    private final String state;
    private final List<String> evidence;

    public ContainerInfo(boolean detected, String state, List<String> evidence) {
        if (state == null || state.isEmpty()) throw new IllegalArgumentException("container state is required");
        if (evidence == null) throw new IllegalArgumentException("container evidence is required");
        this.detected = detected;
        this.state = state;
        this.evidence = List.copyOf(evidence);
    }

    public boolean detected() { return detected; }
    public String state() { return state; }
    public List<String> evidence() { return evidence; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof ContainerInfo)) return false;
        ContainerInfo value = (ContainerInfo) other;
        return detected == value.detected && state.equals(value.state) && evidence.equals(value.evidence);
    }
    @Override public int hashCode() { return Objects.hash(detected, state, evidence); }
}
