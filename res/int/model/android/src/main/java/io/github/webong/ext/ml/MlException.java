package io.github.webong.ext.ml;

public final class MlException extends Exception {
    private static final long serialVersionUID = 1L;
    public enum Kind { INVALID, NOT_FOUND, ALREADY_EXISTS, DIGEST_MISMATCH, UNAVAILABLE, NOT_LOADED, NATIVE_FAILURE }

    public final Kind kind;

    public MlException(Kind kind, String message) {
        super(message);
        this.kind = kind;
    }

    public MlException(Kind kind, String message, Throwable cause) {
        super(message, cause);
        this.kind = kind;
    }
}
