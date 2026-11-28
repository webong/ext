package io.github.webong.ext.system;

import java.util.List;
import java.util.Objects;

public final class SurfaceSnapshot {
    private final String platform;
    private final String coordinateSpace;
    private final String observedAt;
    private final List<DisplayInfo> displays;
    private final List<WindowInfo> windows;
    private final SurfaceCollectionStatus displayStatus;
    private final SurfaceCollectionStatus windowStatus;

    public SurfaceSnapshot(String platform, String coordinateSpace, String observedAt,
            List<DisplayInfo> displays, List<WindowInfo> windows,
            SurfaceCollectionStatus displayStatus, SurfaceCollectionStatus windowStatus) {
        if (platform == null || platform.isEmpty() || coordinateSpace == null || coordinateSpace.isEmpty()
                || !SurfaceWire.timestamp(observedAt) || displays == null || windows == null || displayStatus == null || windowStatus == null) {
            throw new IllegalArgumentException("surface snapshot is incomplete");
        }
        java.util.Set<String> ids = new java.util.HashSet<>();
        int primary = 0;
        for (DisplayInfo display : displays) {
            if (!ids.add(display.id())) throw new IllegalArgumentException("display ids must be unique");
            if (display.primary()) primary++;
        }
        if (primary > 1) throw new IllegalArgumentException("at most one display is primary");
        ids.clear();
        for (WindowInfo window : windows) {
            if (!ids.add(window.id())) throw new IllegalArgumentException("window ids must be unique");
        }
        this.platform = platform;
        this.coordinateSpace = coordinateSpace;
        this.observedAt = observedAt;
        this.displays = List.copyOf(displays);
        this.windows = List.copyOf(windows);
        this.displayStatus = displayStatus;
        this.windowStatus = windowStatus;
    }

    public String platform() { return platform; }
    public String coordinateSpace() { return coordinateSpace; }
    public String observedAt() { return observedAt; }
    public List<DisplayInfo> displays() { return displays; }
    public List<WindowInfo> windows() { return windows; }
    public SurfaceCollectionStatus displayStatus() { return displayStatus; }
    public SurfaceCollectionStatus windowStatus() { return windowStatus; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof SurfaceSnapshot)) return false;
        SurfaceSnapshot value = (SurfaceSnapshot) other;
        return platform.equals(value.platform) && coordinateSpace.equals(value.coordinateSpace)
                && observedAt.equals(value.observedAt) && displays.equals(value.displays) && windows.equals(value.windows)
                && displayStatus.equals(value.displayStatus) && windowStatus.equals(value.windowStatus);
    }
    @Override public int hashCode() { return Objects.hash(platform, coordinateSpace, observedAt, displays, windows, displayStatus, windowStatus); }
}
