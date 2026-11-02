package io.github.webong.ext.system.keyboard;

/** A keyboard failure that a caller can report without exposing native details. */
public final class KeyboardException extends Exception {
    private static final long serialVersionUID = 1L;

    private final String kind;

    public KeyboardException(String kind, String message) {
        super(message);
        this.kind = kind;
    }

    public KeyboardException(String kind, String message, Throwable cause) {
        super(message, cause);
        this.kind = kind;
    }

    public String kind() { return kind; }
}
