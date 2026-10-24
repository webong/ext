package io.github.webong.ext.web;

import java.io.ByteArrayOutputStream;
import java.util.Arrays;

/**
 * Portable, user-activated browser scripts ({@code res/web/bookmarklet}, ext.bookmarklet/v1alpha1).
 * Nothing here evaluates a script or touches a browser's bookmarks.
 */
public final class Bookmarklet {
    private Bookmarklet() {}

    /** Preparation is bounded; a browser's bookmark URL limit may be lower. */
    public static final int MAX_SOURCE_BYTES = 64 * 1024;

    private static final String WRAPPER_START = "void (function(){\n";
    private static final String WRAPPER_END = "\n}).call(window);";
    private static final String PREFIX = "javascript:";

    private static void validate(String source) throws BookmarkletException {
        if (GoText.isBlank(source)) {
            throw new BookmarkletException("bookmarklet source is empty");
        }
        if (GoText.utf8Length(source) > MAX_SOURCE_BYTES) {
            throw new BookmarkletException("bookmarklet source exceeds 64 KiB");
        }
        if (!GoText.isWellFormed(source) || source.indexOf('\0') >= 0) {
            throw new BookmarkletException("bookmarklet source must be UTF-8 without NUL bytes");
        }
    }

    /**
     * Wraps a JavaScript function body so a string result cannot replace the document, and
     * percent-encodes it into a {@code javascript:} URL. Newlines and literal percent signs are kept.
     */
    public static String encode(String source) throws BookmarkletException {
        validate(source);
        if (GoText.lowercased(GoText.trimSpace(source)).startsWith(PREFIX)) {
            throw new BookmarkletException("expected JavaScript source; import a javascript: URL first");
        }
        byte[] code = GoText.utf8(WRAPPER_START + source + WRAPPER_END);
        final String hex = "0123456789ABCDEF";
        StringBuilder out = new StringBuilder(PREFIX);
        for (byte b : code) {
            int c = b & 0xFF;
            boolean unreserved = (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-'
                    || c == '_' || c == '.' || c == '~';
            if (unreserved) {
                out.append((char) c);
            } else {
                out.append('%').append(hex.charAt(c >> 4)).append(hex.charAt(c & 15));
            }
        }
        return out.toString();
    }

    /**
     * Imports a {@code javascript:} URL as source for review, without evaluating it. It decodes
     * exactly once, keeps a literal {@code +} and a {@code %} that is not an escape, and removes
     * only the exact wrapper {@link #encode} writes, so a round trip is lossless.
     */
    public static String decode(String url) throws BookmarkletException {
        byte[] value = GoText.utf8(GoText.trimSpace(url));
        byte[] prefix = GoText.utf8(PREFIX);
        if (value.length > 3 * (MAX_SOURCE_BYTES + 64) + prefix.length) {
            throw new BookmarkletException("bookmarklet URL is too large");
        }
        if (value.length < prefix.length) {
            throw new BookmarkletException("bookmarklet URL must use javascript:");
        }
        for (int i = 0; i < prefix.length; i++) {
            int b = value[i];
            if (b >= 'A' && b <= 'Z') {
                b += 0x20;
            }
            if (b != prefix[i]) {
                throw new BookmarkletException("bookmarklet URL must use javascript:");
            }
        }
        ByteArrayOutputStream decoded = new ByteArrayOutputStream(value.length);
        for (int i = prefix.length; i < value.length; ) {
            if (value[i] == '%' && i + 2 < value.length && UrlEscape.isHex(value[i + 1]) && UrlEscape.isHex(value[i + 2])) {
                decoded.write((UrlEscape.unhex(value[i + 1]) << 4) | UrlEscape.unhex(value[i + 2]));
                i += 3;
            } else {
                decoded.write(value[i]);
                i++;
            }
        }
        byte[] bytes = decoded.toByteArray();
        byte[] start = GoText.utf8(WRAPPER_START);
        byte[] end = GoText.utf8(WRAPPER_END);
        if (startsWith(bytes, start) && endsWith(bytes, end)) {
            // TrimPrefix, then TrimSuffix on what is left (the two can share their newline).
            bytes = Arrays.copyOfRange(bytes, start.length, bytes.length);
            if (endsWith(bytes, end)) {
                bytes = Arrays.copyOfRange(bytes, 0, bytes.length - end.length);
            }
        }
        String source = GoText.decodeStrict(bytes);
        if (source == null) {
            throw new BookmarkletException("bookmarklet source must be UTF-8 without NUL bytes");
        }
        validate(source);
        return source;
    }

    /**
     * A standalone HTML page with a draggable bookmark link and the escaped source for review.
     * There is no script that runs on page load.
     */
    public static String installPage(String name, String source) throws BookmarkletException {
        if (GoText.isBlank(name) || GoText.utf8Length(name) > 256 || !GoText.isWellFormed(name) || name.indexOf('\0') >= 0) {
            throw new BookmarkletException("bookmarklet name must be nonempty UTF-8, at most 256 bytes");
        }
        String link = encode(source);
        return "<!doctype html>\n<html lang=\"en\"><meta charset=\"utf-8\"><title>Install " + escapeHtml(name) + "</title>\n"
                + "<h1>Install " + escapeHtml(name) + "</h1>\n"
                + "<p>Review the source, then drag this link to your bookmarks bar. Open the target page and click the saved bookmark.</p>\n"
                + "<p><a id=\"bookmarklet\" draggable=\"true\" href=\"" + escapeHtml(link) + "\">" + escapeHtml(name) + "</a></p>\n"
                + "<p>No extension process is needed. The script runs with the target page's access. Browser restrictions or the page's content security policy may prevent execution.</p>\n"
                + "<h2>Source</h2><pre>" + escapeHtml(source) + "</pre></html>\n";
    }

    private static boolean startsWith(byte[] bytes, byte[] prefix) {
        return bytes.length >= prefix.length && Arrays.equals(Arrays.copyOfRange(bytes, 0, prefix.length), prefix);
    }

    private static boolean endsWith(byte[] bytes, byte[] suffix) {
        return bytes.length >= suffix.length
                && Arrays.equals(Arrays.copyOfRange(bytes, bytes.length - suffix.length, bytes.length), suffix);
    }

    /** html.EscapeString. */
    private static String escapeHtml(String text) {
        StringBuilder out = new StringBuilder(text.length());
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            switch (c) {
                case '&':
                    out.append("&amp;");
                    break;
                case '<':
                    out.append("&lt;");
                    break;
                case '>':
                    out.append("&gt;");
                    break;
                case '"':
                    out.append("&#34;");
                    break;
                case '\'':
                    out.append("&#39;");
                    break;
                default:
                    out.append(c);
            }
        }
        return out.toString();
    }
}
