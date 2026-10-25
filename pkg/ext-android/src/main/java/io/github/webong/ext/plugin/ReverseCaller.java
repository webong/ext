package io.github.webong.ext.plugin;

import java.time.Duration;

/**
 * Calls an operation the host offers (a {@linkplain Descriptor#reverse() declared} reverse
 * operation) and waits for its answer. Safe to use from several threads at once.
 */
public interface ReverseCaller {
    /**
     * Calls {@code operation} of {@code contract} with a payload (one JSON value, or null for
     * none) and returns the host's payload as one JSON value. The call waits until the host
     * answers, the deadline of the call (or, for a session-scoped caller, 30 seconds) passes
     * or the thread is interrupted.
     *
     * @throws RemoteError the host refused or failed it; the code says why (for example
     *     {@code reverse_undeclared}, {@code denied}, {@code busy}, {@code too_large},
     *     {@code expired})
     * @throws FrameException the call could not be made or its answer did not come
     */
    byte[] call(ContractRef contract, String operation, byte[] payload) throws RemoteError, FrameException;

    /** As {@link #call(ContractRef, String, byte[])}, giving up after {@code timeout} (never past the call's own deadline). */
    byte[] call(ContractRef contract, String operation, byte[] payload, Duration timeout)
            throws RemoteError, FrameException;
}
