package io.github.webong.ext.plugin;

/**
 * An operation a descriptor lists. {@code progress} says the operation reports progress
 * events while it runs ({@link Call#progress}); it is not valid on a host operation in
 * {@link Descriptor#reverse()}.
 */
public record Operation(String name, String surface, boolean progress) {
    public Operation(String name, String surface) {
        this(name, surface, false);
    }

    public Operation(String name) {
        this(name, null, false);
    }

    /** An operation that reports progress. */
    public static Operation reporting(String name) {
        return new Operation(name, null, true);
    }
}
