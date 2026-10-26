package io.github.webong.ext.clipboard;

public final class ClipboardException extends Exception {
    private static final long serialVersionUID = 1L;
    public final String code;
    public ClipboardException(String code, String message) { super(message); this.code = code; }
}
