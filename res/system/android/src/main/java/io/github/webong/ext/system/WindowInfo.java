package io.github.webong.ext.system;

import java.util.Objects;

public final class WindowInfo {
    private final String id;
    private final int pid;
    private final String processStartId;
    private final SurfaceBounds bounds;
    private final boolean onScreen;

    public WindowInfo(String id, int pid, String processStartId, SurfaceBounds bounds, boolean onScreen) {
        if (id == null || id.isEmpty() || pid <= 0 || processStartId == null || processStartId.isEmpty() || bounds == null) {
            throw new IllegalArgumentException("window identity, process generation, and bounds are required");
        }
        this.id = id;
        this.pid = pid;
        this.processStartId = processStartId;
        this.bounds = bounds;
        this.onScreen = onScreen;
    }

    public String id() { return id; }
    public int pid() { return pid; }
    public String processStartId() { return processStartId; }
    public SurfaceBounds bounds() { return bounds; }
    public boolean onScreen() { return onScreen; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof WindowInfo)) return false;
        WindowInfo value = (WindowInfo) other;
        return id.equals(value.id) && pid == value.pid && processStartId.equals(value.processStartId)
                && bounds.equals(value.bounds) && onScreen == value.onScreen;
    }
    @Override public int hashCode() { return Objects.hash(id, pid, processStartId, bounds, onScreen); }
}
