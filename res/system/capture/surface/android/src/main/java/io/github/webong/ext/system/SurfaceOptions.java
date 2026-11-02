package io.github.webong.ext.system;

public final class SurfaceOptions {
    private final int maxDisplays;
    private final int maxWindows;

    public SurfaceOptions() { this(64, 4096); }

    public SurfaceOptions(int maxDisplays, int maxWindows) {
        if (maxDisplays <= 0 || maxDisplays > 65536 || maxWindows <= 0 || maxWindows > 65536) {
            throw new IllegalArgumentException("surface limits must be between 1 and 65536");
        }
        this.maxDisplays = maxDisplays;
        this.maxWindows = maxWindows;
    }

    public int maxDisplays() { return maxDisplays; }
    public int maxWindows() { return maxWindows; }
}
