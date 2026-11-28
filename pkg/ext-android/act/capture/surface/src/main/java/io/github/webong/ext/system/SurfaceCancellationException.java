package io.github.webong.ext.system;

import java.util.concurrent.CancellationException;

/** A stopped observation; {@link #code()} is timeout or canceled (testdata/codes.json), as Go's context errors are. */
public final class SurfaceCancellationException extends CancellationException {
    private static final long serialVersionUID = 1L;
    private final String code;

    public SurfaceCancellationException(String code, String message) {
        super(message);
        this.code = code;
    }

    public String code() { return code; }
}
