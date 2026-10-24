package io.github.webong.ext.web;

/** A site URL that does not parse as an http or https URL with a hostname and no userinfo. */
public final class SiteException extends Exception {
    private static final long serialVersionUID = 1L;

    SiteException() {
        super("site must be an http or https URL with a hostname", null, false, false);
    }
}
