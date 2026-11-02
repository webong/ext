package io.github.webong.ext.system;

import java.util.Objects;

public final class PermissionInfo {
    private final String state;
    private final String scope;
    private final String detail;

    public PermissionInfo(String state, String scope, String detail) {
        if (state == null || state.isEmpty()) throw new IllegalArgumentException("permission state is required");
        this.state = state; this.scope = scope; this.detail = detail;
    }

    public String state() { return state; }
    public String scope() { return scope; }
    public String detail() { return detail; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof PermissionInfo)) return false;
        PermissionInfo value = (PermissionInfo) other;
        return state.equals(value.state) && Objects.equals(scope, value.scope) && Objects.equals(detail, value.detail);
    }
    @Override public int hashCode() { return Objects.hash(state, scope, detail); }
}
