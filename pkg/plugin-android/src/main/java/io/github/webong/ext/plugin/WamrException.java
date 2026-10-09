package io.github.webong.ext.plugin;

/** The engine refused a WAMR host operation. {@link #status()} is the ext_status value. */
public final class WamrException extends Exception {
    private static final long serialVersionUID = 1L;

    /** ext_status values the host reports; the rest are in ext_host.h. */
    public static final int INVALID = 1;
    public static final int DENIED = 2;
    public static final int MISMATCH = 3;
    public static final int UNSUPPORTED = 4;
    public static final int CLOSED = 5;
    public static final int TIMEOUT = 6;

    private final int status;

    public WamrException(int status, String reason) {
        super("ext_status " + status + " (" + reason + ")");
        this.status = status;
    }

    public int status() {
        return status;
    }
}
