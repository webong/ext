package io.github.webong.ext.web;

/** A bookmarklet source, URL or name that the portable rules refuse. */
public final class BookmarkletException extends Exception {
    private static final long serialVersionUID = 1L;

    BookmarkletException(String message) {
        super(message, null, false, false);
    }
}
