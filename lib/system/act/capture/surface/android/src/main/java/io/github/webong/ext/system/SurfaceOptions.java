package io.github.webong.ext.system;

/** Bounds a surface observation: 64 displays, 4096 windows and five seconds by default, as Go's SurfaceOptions. */
public final class SurfaceOptions {
    /** Go's default deadline. */
    public static final long DEFAULT_TIMEOUT_MILLIS = 5_000;
    private final int maxDisplays;
    private final int maxWindows;
    private final long timeoutMillis;

    public SurfaceOptions() { this(64, 4096); }

    public SurfaceOptions(int maxDisplays, int maxWindows) { this(maxDisplays, maxWindows, DEFAULT_TIMEOUT_MILLIS); }

    /** @throws IllegalArgumentException when a limit is outside 1 to 65536 or the timeout is not positive */
    public SurfaceOptions(int maxDisplays, int maxWindows, long timeoutMillis) {
        if (maxDisplays <= 0 || maxDisplays > 65536 || maxWindows <= 0 || maxWindows > 65536) {
            throw new IllegalArgumentException("surface limits must be between 1 and 65536");
        }
        if (timeoutMillis < 1) throw new IllegalArgumentException("surface timeout must be positive");
        this.maxDisplays = maxDisplays;
        this.maxWindows = maxWindows;
        this.timeoutMillis = timeoutMillis;
    }

    public int maxDisplays() { return maxDisplays; }
    public int maxWindows() { return maxWindows; }
    public long timeoutMillis() { return timeoutMillis; }
}
