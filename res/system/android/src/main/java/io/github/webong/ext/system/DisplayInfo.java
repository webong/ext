package io.github.webong.ext.system;

import java.util.Objects;

public final class DisplayInfo {
    private final String id;
    private final SurfaceBounds bounds;
    private final double scale;
    private final boolean primary;

    public DisplayInfo(String id, SurfaceBounds bounds, double scale, boolean primary) {
        if (id == null || id.isEmpty() || bounds == null) throw new IllegalArgumentException("display identity and bounds are required");
        this.id = id;
        this.bounds = bounds;
        this.scale = scale;
        this.primary = primary;
    }

    public String id() { return id; }
    public SurfaceBounds bounds() { return bounds; }
    public double scale() { return scale; }
    public boolean primary() { return primary; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof DisplayInfo)) return false;
        DisplayInfo value = (DisplayInfo) other;
        return id.equals(value.id) && bounds.equals(value.bounds) && scale == value.scale && primary == value.primary;
    }
    @Override public int hashCode() { return Objects.hash(id, bounds, scale, primary); }
}
