package io.github.webong.ext.plugin;

/**
 * Bounds on a package store: on one artifact file (a file inside a tree counts too), on the
 * files in one tree, on the bytes of one tree, and on the archive file that {@link
 * PackageStore#installArchive} reads. Zero takes the engine's default: 4 GiB, 250000 files,
 * 16 GiB and 512 MiB. A negative bound is invalid. An archive install also bounds its entries by
 * {@code maxTreeFiles}, the extracted total by {@code maxTreeBytes} and one file by {@code
 * maxArtifactBytes}. A layered view reads no archive and ignores {@code maxArchiveBytes}.
 */
public record StoreLimits(long maxArtifactBytes, int maxTreeFiles, long maxTreeBytes, long maxArchiveBytes) {
    /** Every bound at the engine's default. */
    public static final StoreLimits DEFAULT = new StoreLimits(0, 0, 0, 0);

    public StoreLimits {
        if (maxArtifactBytes < 0 || maxTreeFiles < 0 || maxTreeBytes < 0 || maxArchiveBytes < 0) {
            throw new IllegalArgumentException("a store limit cannot be negative");
        }
    }
}
