package io.github.webong.ext.plugin;

import java.util.List;
import java.util.Objects;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

/**
 * A read-only view over several package-store roots in priority order, highest first, such as a
 * user root before a system root (see {@code ext_layered_*} in lib/plugin/include/ext_host.h). An
 * id is served from the first root that holds it; a copy in a lower root is shadowed and never
 * read. If the winning copy does not verify, {@link #load} and the reports fail instead of falling
 * back to a lower one. The view never writes, so a system root is never created or modified
 * through it; install, upgrade and remove stay with the {@link PackageStore} the caller owns. It
 * has no default roots. Roots are compared as cleaned paths: a repeated root (however spelled), an
 * empty one, or no root at all is {@link PackageStore#INVALID}.
 *
 * <p>Failures carry the statuses of {@link PackageStore}; {@link PackageStore#NOT_FOUND} means no
 * root holds the id. The reports are those of {@link PackageStore#list()}, {@link
 * PackageStore#verify()} and {@link PackageStore#listUnverified()} over all the roots, one line
 * per id from the root that serves it, and carry the position of that root as {@code layer}. Safe
 * to share between threads.
 */
public final class LayeredPackageStore implements AutoCloseable {
    /** A package read through the view: its {@code plugin.json} and the position of the root that served it. */
    public record Loaded(byte[] manifest, int layer) {}

    private final ReadWriteLock lock = new ReentrantReadWriteLock();
    private long handle;

    /** Opens a view over {@code roots}, which need not exist, with the engine's default limits. */
    public LayeredPackageStore(List<String> roots) throws GuestException {
        this(roots, StoreLimits.DEFAULT);
    }

    /** Opens a view over {@code roots}, highest priority first, which need not exist. */
    public LayeredPackageStore(List<String> roots, StoreLimits limits) throws GuestException {
        Objects.requireNonNull(roots, "roots");
        Objects.requireNonNull(limits, "limits");
        byte[][] paths = new byte[roots.size()][];
        for (int i = 0; i < paths.length; i++) {
            paths[i] = PackageStore.text(Objects.requireNonNull(roots.get(i), "root"));
        }
        handle = NativeEngine.layeredOpen(paths, limits.maxArtifactBytes(), limits.maxTreeFiles(),
                limits.maxTreeBytes());
    }

    /**
     * Reads and verifies the package from the first root that holds {@code id} and returns its
     * {@code plugin.json} and the position of that root. {@link PackageStore#NOT_FOUND} means no
     * root holds it.
     */
    public Loaded load(String id) throws GuestException {
        byte[] name = PackageStore.text(Objects.requireNonNull(id, "id"));
        int[] layer = new int[1];
        lock.readLock().lock();
        try {
            byte[] manifest = NativeEngine.layeredLoad(live(), name, layer);
            return new Loaded(manifest, layer[0]);
        } finally {
            lock.readLock().unlock();
        }
    }

    /** As {@link PackageStore#list()} over every root, one line per id from the root that serves it. */
    public byte[] list() throws GuestException {
        return report(NativeEngine.REPORT_LIST);
    }

    /** As {@link PackageStore#verify()} over every root. */
    public byte[] verify() throws GuestException {
        return report(NativeEngine.REPORT_VERIFY);
    }

    /** As {@link PackageStore#listUnverified()} over every root; the result is unverified. */
    public byte[] listUnverified() throws GuestException {
        return report(NativeEngine.REPORT_UNVERIFIED);
    }

    /** Waits for the calls in flight, then releases the view. Idempotent; later calls fail with {@link PackageStore#CLOSED}. */
    @Override
    public void close() {
        lock.writeLock().lock();
        try {
            if (handle != 0) {
                NativeEngine.layeredClose(handle);
                handle = 0;
            }
        } finally {
            lock.writeLock().unlock();
        }
    }

    private byte[] report(int kind) throws GuestException {
        lock.readLock().lock();
        try {
            return NativeEngine.layeredReport(live(), kind);
        } finally {
            lock.readLock().unlock();
        }
    }

    private long live() throws GuestException {
        if (handle == 0) {
            throw new GuestException(PackageStore.CLOSED);
        }
        return handle;
    }
}
