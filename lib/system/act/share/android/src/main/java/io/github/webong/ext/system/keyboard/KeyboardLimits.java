package io.github.webong.ext.system.keyboard;

/** Bounds for one keyboard-extension editor session. */
public final class KeyboardLimits {
    public static final int MAX_SELECTION_CHARS = 20_000;
    public static final int MAX_INSERT_CHARS = 10_000;

    public final int maxSelectionChars;
    public final int maxInsertChars;

    public KeyboardLimits() {
        this(MAX_SELECTION_CHARS, MAX_INSERT_CHARS);
    }

    public KeyboardLimits(int maxSelectionChars, int maxInsertChars) {
        if (maxSelectionChars < 1 || maxSelectionChars > MAX_SELECTION_CHARS
                || maxInsertChars < 1 || maxInsertChars > MAX_INSERT_CHARS) {
            throw new IllegalArgumentException("keyboard limits must be positive and within library ceilings");
        }
        this.maxSelectionChars = maxSelectionChars;
        this.maxInsertChars = maxInsertChars;
    }
}
