package io.github.webong.ext.system;

import java.util.List;
import java.util.Objects;

public final class ContainerInfo {
    private final boolean detected;
    private final String state;
    private final List<String> evidence;

    public ContainerInfo(boolean detected, String state, List<String> evidence) {
        if (!"detected".equals(state) && !"none".equals(state) && !"unsupported".equals(state)) {
            throw new IllegalArgumentException("container state must be detected, none or unsupported");
        }
        if (evidence == null) throw new IllegalArgumentException("container evidence is required");
        if (detected != "detected".equals(state) || detected != !evidence.isEmpty()) {
            throw new IllegalArgumentException("container detection, state and evidence disagree");
        }
        for (int index = 0; index < evidence.size(); index++) {
            String name = evidence.get(index);
            if (name == null || name.isEmpty() || index > 0 && utf8Compare(name, evidence.get(index - 1)) <= 0) {
                throw new IllegalArgumentException("container evidence must be non-empty names, sorted and unique");
            }
        }
        this.detected = detected;
        this.state = state;
        this.evidence = List.copyOf(evidence);
    }

    /** Compares by UTF-8 bytes (that is, by code point), as the other bindings sort. */
    private static int utf8Compare(String a, String b) {
        int i = 0;
        int j = 0;
        while (i < a.length() && j < b.length()) {
            int x = a.codePointAt(i);
            int y = b.codePointAt(j);
            if (x != y) return Integer.compare(x, y);
            i += Character.charCount(x);
            j += Character.charCount(y);
        }
        return Integer.compare(a.length() - i, b.length() - j);
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
