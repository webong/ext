package io.github.webong.ext.web;

/** A cookie or bundle that breaks the portable rules. The message never contains a cookie value. */
public final class CookieException extends Exception {
    private static final long serialVersionUID = 1L;

    public CookieException(String message) {
        super(message, null, false, false);
    }
}
