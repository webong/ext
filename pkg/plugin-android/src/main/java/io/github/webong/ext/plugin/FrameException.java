package io.github.webong.ext.plugin;

/**
 * A progress or reverse-call operation failed locally. A failure of the host's answer is a
 * {@link RemoteError} instead.
 */
public final class FrameException extends Exception {
    private static final long serialVersionUID = 1L;

    /** Why the operation failed. */
    public enum Code {
        /** No frame channel on this call: the operation did not declare progress, or the transport has none. */
        UNSUPPORTED,
        /** The event or request breaks the rules; nothing was sent. */
        INVALID,
        /** The call is over, or the connection ended. */
        CLOSED,
        /** The reverse call's deadline passed before the host answered. */
        DEADLINE,
        /** The waiting thread was interrupted. */
        CANCELED,
        /** The host broke the framing rules; the session ends. */
        PROTOCOL
    }

    private final Code code;

    FrameException(Code code) {
        super(code.name().toLowerCase(java.util.Locale.ROOT));
        this.code = code;
    }

    public Code code() {
        return code;
    }
}
