package io.github.webong.ext.system;

import java.util.Objects;

public final class PermissionInfo {
    private final String state;
    private final String reason;
    private final String scope;
    private final String detail;

    public PermissionInfo(String state, String scope, String detail) {
        this(state, null, scope, detail);
    }

    /**
     * A state (ready, denied, not-determined, not-required, unsupported or unavailable) and, for
     * unsupported and unavailable, why: unsupported, permission, no_device or tool_missing
     * (testdata/capability/v1).
     *
     * @throws IllegalArgumentException when the state is unknown or the reason does not fit it
     */
    public PermissionInfo(String state, String reason, String scope, String detail) {
        if (state == null) throw new IllegalArgumentException("permission state is required");
        switch (state) {
            case "ready":
            case "denied":
            case "not-determined":
            case "not-required":
                if (reason != null) throw new IllegalArgumentException("permission state takes no reason");
                break;
            case "unsupported":
                if (!"unsupported".equals(reason) && !"tool_missing".equals(reason)) {
                    throw new IllegalArgumentException("permission reason does not fit its state");
                }
                break;
            case "unavailable":
                if (reason != null && !"permission".equals(reason) && !"no_device".equals(reason) && !"tool_missing".equals(reason)) {
                    throw new IllegalArgumentException("permission reason does not fit its state");
                }
                break;
            default:
                throw new IllegalArgumentException("permission state is unknown");
        }
        this.state = state; this.reason = reason; this.scope = scope; this.detail = detail;
    }

    public String state() { return state; }
    /** Why something is unsupported or unavailable, or null. */
    public String reason() { return reason; }
    public String scope() { return scope; }
    public String detail() { return detail; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof PermissionInfo)) return false;
        PermissionInfo value = (PermissionInfo) other;
        return state.equals(value.state) && Objects.equals(reason, value.reason) && Objects.equals(scope, value.scope)
            && Objects.equals(detail, value.detail);
    }
    @Override public int hashCode() { return Objects.hash(state, reason, scope, detail); }
}
