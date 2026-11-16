package io.github.webong.ext.system;

import java.util.concurrent.TimeUnit;

/**
 * Stops a resource observation the way a Go context stops {@code DiscoverResources}:
 * {@link #cancel()} ends it with the code {@code canceled}, and a deadline that passes ends it with
 * {@code timeout} (testdata/codes.json). A child made by {@link #withTimeout(long)} ends when its parent
 * does or when its own deadline passes. The first stop is kept: canceling after a timeout is still a
 * timeout. Observation is synchronous and checks between steps, so a stop takes effect at the next check.
 * It is safe to cancel from another thread.
 */
public final class ResourceCancellation {
    private final ResourceCancellation parent;
    private final long started;
    private final long timeoutNanos;
    private volatile String code;

    /** A cancellation with no deadline. */
    public ResourceCancellation() { this(null, -1); }

    private ResourceCancellation(ResourceCancellation parent, long timeoutNanos) {
        this.parent = parent;
        this.started = System.nanoTime();
        this.timeoutNanos = timeoutNanos;
    }

    /**
     * A child that also times out {@code timeoutMillis} milliseconds from now.
     *
     * @throws IllegalArgumentException when the timeout is not positive
     */
    public ResourceCancellation withTimeout(long timeoutMillis) {
        if (timeoutMillis < 1) throw new IllegalArgumentException("resource timeout must be positive");
        return new ResourceCancellation(this, TimeUnit.MILLISECONDS.toNanos(timeoutMillis));
    }

    /** Stops the observation with the code canceled, unless it has already stopped. */
    public void cancel() { stop("canceled"); }

    /** canceled, timeout, or null while the observation may go on. */
    public String code() {
        String current = code;
        if (current != null) return current;
        String inherited = parent == null ? null : parent.code();
        if (inherited != null) return stop(inherited);
        if (timeoutNanos >= 0 && System.nanoTime() - started >= timeoutNanos) return stop("timeout");
        return null;
    }

    /**
     * Returns when the observation may go on.
     *
     * @throws ResourceCancellationException with the code timeout or canceled once it has stopped
     */
    public void check() {
        String current = code();
        if (current == null) return;
        throw new ResourceCancellationException(current,
                "timeout".equals(current) ? "resource observation timed out" : "resource observation was canceled");
    }

    private synchronized String stop(String value) {
        if (code == null) code = value;
        return code;
    }
}
