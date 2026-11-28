package io.github.webong.ext.web;

/**
 * A record that breaks ext.userscript/v1alpha1. {@link #code()} is stable and language-neutral
 * (it is what the fixtures check); the message is for people. A failure that is not a record rule,
 * such as an empty revision passed to {@link Userscript#replaySource}, has the code {@code ""}.
 */
public final class UserscriptException extends Exception {
    private static final long serialVersionUID = 1L;

    private final String code;

    UserscriptException(String code, String message) {
        super(message, null, false, false);
        this.code = code;
    }

    public String code() {
        return code;
    }
}
