package io.github.webong.ext.system;

/**
 * Bounds an accessibility observation: 1024 elements, a depth of 16 and five seconds by default, as Go's
 * AccessibilityOptions.
 */
public final class AccessibilityOptions {
    /** Go's default deadline. */
    public static final long DEFAULT_TIMEOUT_MILLIS = 5_000;
    private final int maxElements;
    private final int maxDepth;
    private final boolean includeText;
    private final long timeoutMillis;

    public AccessibilityOptions(int maxElements, int maxDepth, boolean includeText) {
        this(maxElements, maxDepth, includeText, DEFAULT_TIMEOUT_MILLIS);
    }

    /** @throws IllegalArgumentException when a limit exceeds its range or the timeout is not positive */
    public AccessibilityOptions(int maxElements, int maxDepth, boolean includeText, long timeoutMillis) {
        if (maxElements < 1 || maxElements > 65536 || maxDepth < 1 || maxDepth > 256) {
            throw new IllegalArgumentException("accessibility limits exceed the supported range");
        }
        if (timeoutMillis < 1) throw new IllegalArgumentException("accessibility timeout must be positive");
        this.maxElements = maxElements; this.maxDepth = maxDepth; this.includeText = includeText;
        this.timeoutMillis = timeoutMillis;
    }

    public AccessibilityOptions() { this(1024, 16, false); }
    public int maxElements() { return maxElements; }
    public int maxDepth() { return maxDepth; }
    public boolean includeText() { return includeText; }
    public long timeoutMillis() { return timeoutMillis; }
}
