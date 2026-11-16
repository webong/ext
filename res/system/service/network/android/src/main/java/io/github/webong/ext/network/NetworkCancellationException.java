package io.github.webong.ext.network;

import java.util.concurrent.CancellationException;

/** A stopped observation; {@link #code()} is timeout or canceled (testdata/codes.json), as Go's context errors are. */
public final class NetworkCancellationException extends CancellationException {
    private static final long serialVersionUID = 1L;
    private final String code;

    public NetworkCancellationException(String code, String message) {
        super(message);
        this.code = code;
    }

    public String code() { return code; }
}
