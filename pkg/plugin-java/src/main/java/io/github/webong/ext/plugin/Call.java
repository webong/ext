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
