package io.github.webong.ext.web;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;

/**
 * Text rules that follow Go's strings, unicode and utf8 packages. The ext.web fixtures are
 * defined by Go's behavior, which counts UTF-8 bytes and Unicode scalars; Java counts UTF-16 code
 * units, so lengths and case folding are written out here.
 *
 * <p>Go never sees an unpaired surrogate (its decoders turn one into U+FFFD). A Java string that
 * has one is treated as not valid UTF-8 wherever the rules ask for that.
 */
final class GoText {
    private GoText() {}

    /** unicode.IsSpace. */
    static boolean isSpace(int c) {
        return (c >= 0x09 && c <= 0x0D) || c == 0x20 || c == 0x85 || c == 0xA0 || c == 0x1680
                || (c >= 0x2000 && c <= 0x200A) || c == 0x2028 || c == 0x2029 || c == 0x202F
                || c == 0x205F || c == 0x3000;
    }

    /** strings.TrimSpace. */
    static String trimSpace(String text) {
        int start = 0;
        int end = text.length();
        while (start < end) {
            int c = text.codePointAt(start);
            if (!isSpace(c)) {
                break;
            }
            start += Character.charCount(c);
        }
        while (end > start) {
            int c = text.codePointBefore(end);
            if (!isSpace(c)) {
                break;
            }
            end -= Character.charCount(c);
        }
        return text.substring(start, end);
    }

    /** strings.TrimSpace(text) is empty. */
    static boolean isBlank(String text) {
        return trimSpace(text).isEmpty();
    }

    /** If {@code text} starts with {@code prefix}, the rest; otherwise null (strings.CutPrefix). */
    static String cutPrefix(String text, String prefix) {
        return text.startsWith(prefix) ? text.substring(prefix.length()) : null;
    }

    /** strings.Split on one character. */
    static List<String> split(String text, char separator) {
        List<String> parts = new ArrayList<>();
        int from = 0;
        while (true) {
            int at = text.indexOf(separator, from);
            if (at < 0) {
                parts.add(text.substring(from));
                return parts;
            }
            parts.add(text.substring(from, at));
            from = at + 1;
        }
    }

    /** Any character below U+0020, or U+007F. */
    static boolean hasControl(String text) {
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (c < 0x20 || c == 0x7F) {
                return true;
            }
        }
        return false;
    }

    /** strings.ContainsAny for a set of BMP characters. */
    static boolean containsAny(String text, String characters) {
        for (int i = 0; i < text.length(); i++) {
            if (characters.indexOf(text.charAt(i)) >= 0) {
                return true;
            }
        }
        return false;
    }

    /** True when the text has no unpaired surrogate, so it is valid UTF-8 once encoded. */
    static boolean isWellFormed(String text) {
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (Character.isHighSurrogate(c)) {
                if (i + 1 >= text.length() || !Character.isLowSurrogate(text.charAt(i + 1))) {
                    return false;
                }
                i++;
            } else if (Character.isLowSurrogate(c)) {
                return false;
            }
        }
        return true;
    }

    /** len(text) in Go: the UTF-8 length. An unpaired surrogate counts as U+FFFD, three bytes. */
    static int utf8Length(String text) {
        int length = 0;
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (c < 0x80) {
                length += 1;
            } else if (c < 0x800) {
                length += 2;
            } else if (Character.isHighSurrogate(c) && i + 1 < text.length() && Character.isLowSurrogate(text.charAt(i + 1))) {
                length += 4;
                i++;
            } else {
                length += 3;
            }
        }
        return length;
    }

    static byte[] utf8(String text) {
        return text.getBytes(StandardCharsets.UTF_8);
    }

    /** unicode.ToLower, the simple one-to-one mapping. */
    static int lowercase(int c) {
        return Character.toLowerCase(c);
    }

    /** strings.ToLower. */
    static String lowercased(String text) {
        StringBuilder out = new StringBuilder(text.length());
        for (int i = 0; i < text.length(); ) {
            int c = text.codePointAt(i);
            out.appendCodePoint(lowercase(c));
            i += Character.charCount(c);
        }
        return out.toString();
    }

    /**
     * Bytes to text the way Go's range loop and strings.ToLower see them: each byte that does not
     * start a valid sequence becomes U+FFFD on its own.
     */
    static String decodeLossy(byte[] bytes) {
        StringBuilder out = new StringBuilder(bytes.length);
        int index = 0;
        while (index < bytes.length) {
            int length = sequenceLength(bytes, index);
            if (length == 0) {
                out.append('�');
                index += 1;
            } else {
                out.appendCodePoint(scalar(bytes, index, length));
                index += length;
            }
        }
        return out.toString();
    }

    /** The text of valid UTF-8, or null: the check utf8.Valid makes. */
    static String decodeStrict(byte[] bytes) {
        int index = 0;
        while (index < bytes.length) {
            int length = sequenceLength(bytes, index);
            if (length == 0) {
                return null;
            }
            index += length;
        }
        return new String(bytes, StandardCharsets.UTF_8);
    }

    private static boolean continuation(byte[] bytes, int index, int low, int high) {
        if (index >= bytes.length) {
            return false;
        }
        int b = bytes[index] & 0xFF;
        return b >= low && b <= high;
    }

    /** The length of the valid sequence at {@code index}, or 0. */
    private static int sequenceLength(byte[] bytes, int index) {
        int first = bytes[index] & 0xFF;
        if (first < 0x80) {
            return 1;
        }
        if (first >= 0xC2 && first <= 0xDF) {
            return continuation(bytes, index + 1, 0x80, 0xBF) ? 2 : 0;
        }
        if (first >= 0xE0 && first <= 0xEF) {
            int low = first == 0xE0 ? 0xA0 : 0x80;
            int high = first == 0xED ? 0x9F : 0xBF;
            return continuation(bytes, index + 1, low, high) && continuation(bytes, index + 2, 0x80, 0xBF) ? 3 : 0;
        }
        if (first >= 0xF0 && first <= 0xF4) {
            int low = first == 0xF0 ? 0x90 : 0x80;
            int high = first == 0xF4 ? 0x8F : 0xBF;
            return continuation(bytes, index + 1, low, high) && continuation(bytes, index + 2, 0x80, 0xBF)
                            && continuation(bytes, index + 3, 0x80, 0xBF)
                    ? 4
                    : 0;
        }
        return 0;
    }

    private static int scalar(byte[] bytes, int index, int length) {
        int first = bytes[index] & 0xFF;
        switch (length) {
            case 1:
                return first;
            case 2:
                return ((first & 0x1F) << 6) | (bytes[index + 1] & 0x3F);
            case 3:
                return ((first & 0x0F) << 12) | ((bytes[index + 1] & 0x3F) << 6) | (bytes[index + 2] & 0x3F);
            default:
                return ((first & 0x07) << 18) | ((bytes[index + 1] & 0x3F) << 12) | ((bytes[index + 2] & 0x3F) << 6)
                        | (bytes[index + 3] & 0x3F);
        }
    }

    /** The JSON string Go's encoding/json writes: {@code <}, {@code >}, {@code &}, U+2028 and U+2029 escaped. */
    static StringBuilder jsonQuote(StringBuilder out, String text) {
        final String hex = "0123456789abcdef";
        out.append('"');
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            switch (c) {
                case '"':
                    out.append("\\\"");
                    break;
                case '\\':
                    out.append("\\\\");
                    break;
                case '\b':
                    out.append("\\b");
                    break;
                case '\f':
                    out.append("\\f");
                    break;
                case '\n':
                    out.append("\\n");
                    break;
                case '\r':
                    out.append("\\r");
                    break;
                case '\t':
                    out.append("\\t");
                    break;
                default:
                    if (c < 0x20 || c == '<' || c == '>' || c == '&' || c == 0x2028 || c == 0x2029) {
                        out.append("\\u").append(hex.charAt((c >> 12) & 15)).append(hex.charAt((c >> 8) & 15))
                                .append(hex.charAt((c >> 4) & 15)).append(hex.charAt(c & 15));
                    } else if (Character.isHighSurrogate(c) && i + 1 < text.length() && Character.isLowSurrogate(text.charAt(i + 1))) {
                        out.append(c).append(text.charAt(++i));
                    } else if (Character.isSurrogate(c)) {
                        out.append('�');
                    } else {
                        out.append(c);
                    }
            }
        }
        return out.append('"');
    }

    /** A string array; null is {@code null}, which differs from {@code []} (and has a different digest). */
    static StringBuilder jsonArray(StringBuilder out, List<String> items) {
        if (items == null) {
            return out.append("null");
        }
        out.append('[');
        for (int i = 0; i < items.size(); i++) {
            if (i > 0) {
                out.append(',');
            }
            jsonQuote(out, items.get(i));
        }
        return out.append(']');
    }
}
