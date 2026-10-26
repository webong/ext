package io.github.webong.ext.system;

public final class AccessibilityOptions {
    private final int maxElements;
    private final int maxDepth;
    private final boolean includeText;

    public AccessibilityOptions(int maxElements, int maxDepth, boolean includeText) {
        if (maxElements < 1 || maxElements > 65536 || maxDepth < 1 || maxDepth > 256) {
            throw new IllegalArgumentException("accessibility limits exceed the supported range");
        }
        this.maxElements = maxElements; this.maxDepth = maxDepth; this.includeText = includeText;
    }

    public AccessibilityOptions() { this(1024, 16, false); }
    public int maxElements() { return maxElements; }
    public int maxDepth() { return maxDepth; }
    public boolean includeText() { return includeText; }
}
