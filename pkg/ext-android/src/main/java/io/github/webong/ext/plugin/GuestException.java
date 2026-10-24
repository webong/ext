package io.github.webong.ext.plugin;

/** The engine rejected an operation. {@link #status()} is the ext_status value. */
public final class GuestException extends Exception {
    private static final long serialVersionUID = 1L;

    private final int status;

    public GuestException(int status) {
        super("plugin engine status " + status);
        this.status = status;
    }

    public int status() {
        return status;
    }
}
