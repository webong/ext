package io.github.webong.ext.web;

import java.io.ByteArrayOutputStream;
import java.util.Arrays;

/** net/url's percent-encoding rules for the parts of a URL a site has. */
final class UrlEscape {
    private UrlEscape() {}

    enum Mode {
        PATH,
        HOST,
        ZONE,
        FRAGMENT
    }

    /** Thrown for text the rules refuse; the caller turns it into a {@link SiteException}. */
    static final class Invalid extends Exception {
        private static final long serialVersionUID = 1L;

        Invalid() {
            super(null, null, false, false);
        }
    }

    static boolean isHex(byte b) {
        return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F');
    }

    static int unhex(byte b) {
        return b >= 'a' ? b - 'a' + 10 : b >= 'A' ? b - 'A' + 10 : b - '0';
    }

    static boolean shouldEscape(int c, Mode mode) {
        if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
            return false;
        }
        if (mode == Mode.HOST || mode == Mode.ZONE) {
            switch (c) {
                case '!': case '$': case '&': case '\'': case '(': case ')': case '*': case '+': case ',':
                case ';': case '=': case ':': case '[': case ']': case '<': case '>': case '"':
                    return false;
                default:
                    break;
            }
        }
        switch (c) {
            case '-': case '_': case '.': case '~':
                return false;
            case '$': case '&': case '+': case ',': case '/': case ':': case ';': case '=': case '?': case '@':
                if (mode == Mode.PATH) {
                    return c == '?';
                }
                if (mode == Mode.FRAGMENT) {
                    return false;
                }
                break;
            default:
                break;
        }
        if (mode == Mode.FRAGMENT) {
            switch (c) {
                case '!': case '(': case ')': case '*':
                    return false;
                default:
                    break;
            }
        }
        return true;
    }

    static byte[] unescape(byte[] text, Mode mode) throws Invalid {
        int escapes = 0;
        for (int i = 0; i < text.length; ) {
            byte b = text[i];
            if (b == '%') {
                escapes++;
                if (i + 2 >= text.length || !isHex(text[i + 1]) || !isHex(text[i + 2])) {
                    throw new Invalid();
                }
                boolean isPercent = text[i + 1] == '2' && text[i + 2] == '5';
                // In a host, %-encoding is for non-ASCII bytes only (and %25 in a zone).
                if (mode == Mode.HOST && unhex(text[i + 1]) < 8 && !isPercent) {
                    throw new Invalid();
                }
                if (mode == Mode.ZONE) {
                    int value = (unhex(text[i + 1]) << 4) | unhex(text[i + 2]);
                    if (!isPercent && value != 0x20 && shouldEscape(value, Mode.HOST)) {
                        throw new Invalid();
                    }
                }
                i += 3;
            } else {
                if ((mode == Mode.HOST || mode == Mode.ZONE) && b >= 0 && shouldEscape(b, mode)) {
                    throw new Invalid();
                }
                i++;
            }
        }
        if (escapes == 0) {
            return text;
        }
        ByteArrayOutputStream out = new ByteArrayOutputStream(text.length);
        for (int i = 0; i < text.length; ) {
            if (text[i] == '%') {
                out.write((unhex(text[i + 1]) << 4) | unhex(text[i + 2]));
                i += 3;
            } else {
                out.write(text[i]);
                i++;
            }
        }
        return out.toByteArray();
    }

    static byte[] escape(byte[] text, Mode mode) {
        final byte[] upper = {'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'A', 'B', 'C', 'D', 'E', 'F'};
        ByteArrayOutputStream out = new ByteArrayOutputStream(text.length);
        for (byte b : text) {
            // A byte of 0x80 or more is negative here and always escaped.
            if (b < 0 || shouldEscape(b, mode)) {
                out.write('%');
                out.write(upper[(b >> 4) & 15]);
                out.write(upper[b & 15]);
            } else {
                out.write(b);
            }
        }
        return out.toByteArray();
    }

    static boolean validEncoded(byte[] text, Mode mode) {
        for (byte b : text) {
            switch (b) {
                case '!': case '$': case '&': case '\'': case '(': case ')': case '*': case '+': case ',':
                case ';': case '=': case ':': case '@': case '[': case ']': case '%':
                    continue;
                default:
                    if (b < 0 || shouldEscape(b, mode)) {
                        return false;
                    }
            }
        }
        return true;
    }

    static boolean same(byte[] a, byte[] b) {
        return Arrays.equals(a, b);
    }
}
