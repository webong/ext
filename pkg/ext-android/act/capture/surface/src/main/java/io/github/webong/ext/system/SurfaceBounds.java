package io.github.webong.ext.system;

import java.util.Objects;

public final class SurfaceBounds {
    private final int x;
    private final int y;
    private final int width;
    private final int height;

    public SurfaceBounds(int x, int y, int width, int height) {
        if (width <= 0 || height <= 0) throw new IllegalArgumentException("surface bounds must be positive");
        this.x = x;
        this.y = y;
        this.width = width;
        this.height = height;
    }

    public int x() { return x; }
    public int y() { return y; }
    public int width() { return width; }
    public int height() { return height; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof SurfaceBounds)) return false;
        SurfaceBounds value = (SurfaceBounds) other;
        return x == value.x && y == value.y && width == value.width && height == value.height;
    }
    @Override public int hashCode() { return Objects.hash(x, y, width, height); }
}
