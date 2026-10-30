package io.github.webong.ext.plugin;

/**
 * Bounds on a package store: on one artifact file (a file inside a tree counts too), on the
 * files in one tree and on the bytes of one tree. Zero takes the engine's default: 4 GiB,
 * 250000 files and 16 GiB. A negative bound is invalid.
 */
public record StoreLimits(long maxArtifactBytes, int maxTreeFiles, long maxTreeBytes) {
    /** Every bound at the engine's default. */
    public static final StoreLimits DEFAULT = new StoreLimits(0, 0, 0);

    public StoreLimits {
        if (maxArtifactBytes < 0 || maxTreeFiles < 0 || maxTreeBytes < 0) {
            throw new IllegalArgumentException("a store limit cannot be negative");
        }
    }
}
