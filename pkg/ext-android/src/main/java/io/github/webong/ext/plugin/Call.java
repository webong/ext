package io.github.webong.ext.plugin;

import java.util.concurrent.TimeUnit;

/** One admitted invocation. The engine has already validated it against the descriptor. */
public final class Call {
    private final String id;
    private final Identity plugin;
    private final ContractRef contract;
    private final String operation;
    private final String surface;
    private final byte[] payload;
    private final long expiryNanos;
    private Connection connection;
    private Reporter reporter;
    private ProgressLimits limits;
    private ReverseCaller reverse;
    private volatile boolean over;

    Call(String id, Identity plugin, ContractRef contract, String operation, String surface,
         byte[] payload, long remainingMilliseconds) {
        this.id = id;
        this.plugin = plugin;
        this.contract = contract;
        this.operation = operation;
        this.surface = surface;
        this.payload = payload;
        this.expiryNanos = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(remainingMilliseconds);
    }

    public String id() {
        return id;
    }

    public Identity plugin() {
        return plugin;
    }

    public ContractRef contract() {
        return contract;
    }

    public String operation() {
        return operation;
    }

    /** The admission surface, or null. */
    public String surface() {
        return surface;
    }

    /**
     * The request payload exactly as the caller sent it: one JSON value, or an
     * empty array when the request had none. Numbers keep their original text.
     */
    public byte[] payload() {
        return payload.clone();
    }

    /**
     * Reports a progress event to the host while the call runs. The events reach the host in
     * the order they are reported, before the result, within the rate limit the host set;
     * {@link ProgressOutcome#DROPPED} is routine, and the next event that passes carries the
     * count. Safe to call from several threads.
     *
     * @throws FrameException {@code UNSUPPORTED} when the operation did not declare progress,
     *     the host did not ask for events or the transport has no frames (stop reporting);
     *     {@code INVALID} for an event that breaks the rules (nothing is sent); {@code CLOSED}
     *     once the call is over or the connection ended
     */
    public ProgressOutcome progress(Progress event) throws FrameException {
        Reporter current = reporter;
        if (current == null) {
            throw new FrameException(FrameException.Code.UNSUPPORTED);
        }
        return current.report(event);
    }

    /** The rate the host set for this call's events, or null when it reports none. */
    public ProgressLimits progressLimits() {
        return limits;
    }

    /**
     * Calls an operation the host offers (declared in {@link Descriptor#reverse()}) on behalf
     * of this call, bounded by its deadline; or null on a transport with no frames. Several
     * calls may be outstanding at once.
     */
    public ReverseCaller reverse() {
        return reverse;
    }

    /**
     * Calls a host operation on behalf of the session rather than this call, so background work
     * may keep using it after the call returned; or null on a transport with no frames.
     */
    public ReverseCaller sessionReverse() {
        Connection current = connection;
        return current == null ? null : current.session();
    }

    /**
     * True once the connection to the host has ended (it closed its end or broke the rules):
     * a handler that polls this stops early. A reverse call that is waiting is released.
     */
    public boolean connectionEnded() {
        Connection current = connection;
        return current != null && current.ended();
    }

    /** Binds the call to a connection that carries frames. */
    void attach(Connection connection, Reporter reporter, ProgressLimits limits, long deadlineMillis) {
        this.connection = connection;
        this.reporter = reporter;
        this.limits = limits;
        this.reverse = connection.forCall(id, deadlineMillis, this);
    }

    /** The handler returned: closes progress with a marker for events never reported. */
    void finish() {
        over = true;
        if (reporter != null) {
            reporter.finish();
        }
    }

    boolean over() {
        return over;
    }

    public long remainingMilliseconds() {
        long left = expiryNanos - System.nanoTime();
        return left <= 0 ? 0 : TimeUnit.NANOSECONDS.toMillis(left);
    }

    /** Throws once the call's time is spent. */
    public void checkDeadline() throws DeadlineExceeded {
        if (expiryNanos - System.nanoTime() <= 0) {
            throw new DeadlineExceeded();
        }
    }

    /** Thrown by {@link #checkDeadline()}. */
    public static final class DeadlineExceeded extends Exception {
        private static final long serialVersionUID = 1L;

        DeadlineExceeded() {
            super("deadline exceeded");
        }
    }
}
