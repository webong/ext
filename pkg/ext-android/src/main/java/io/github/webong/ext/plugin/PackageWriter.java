package io.github.webong.ext.plugin;

import java.util.Objects;

/**
 * Owns the native cross-process writer lock for one EXT_HOME. Use try-with-resources
 * around the complete read/check/update operation, including approval changes.
 * Acquisition fails immediately with {@link PackageStore#EXISTS} on contention.
 * Existing locks are never recovered by age; abrupt termination leaves the lock.
 * The home must be protected against hostile mutation.
 */
public final class PackageWriter implements AutoCloseable {
    private long handle;

    /** Pass EXT_HOME, not its package-store subdirectory. Creates the home if needed. */
    public PackageWriter(String home) throws GuestException {
        handle = NativeEngine.storeWriterAcquire(PackageStore.text(Objects.requireNonNull(home, "home")));
    }

    /**
     * Releases once. A replaced lock is preserved and reported as MISMATCH.
     * The native handle is consumed even if release fails. Concurrent closes are safe.
     */
    @Override
    public synchronized void close() throws GuestException {
        if (handle == 0) return;
        long owned = handle;
        handle = 0;
        NativeEngine.storeWriterClose(owned);
    }
}
