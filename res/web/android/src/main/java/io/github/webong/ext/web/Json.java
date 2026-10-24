package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * The little JSON the wire types need, so the library has no dependency (the JDK has no JSON
 * parser, and Android's {@code org.json} is not on a desktop JDK). Objects become {@link Map},
 * arrays {@link List}, integers {@link Long}, other numbers {@link Double}, and null stays null. A
 * lone surrogate escape becomes U+FFFD, as Go's decoder reads it.
 */
final class Json {
    private Json() {}

    static Object parse(String text) {
        Parser parser = new Parser(text);
        parser.space();
        Object value = parser.value();
        parser.space();
        if (parser.at != text.length()) {
            throw new IllegalArgumentException("trailing data in JSON");
        }
        return value;
    }

    @SuppressWarnings("unchecked")
    static Map<String, Object> object(Object value, String what) {
        if (!(value instanceof Map)) {
            throw new IllegalArgumentException(what + " must be a JSON object");
        }
        return (Map<String, Object>) value;
    }

    /** A string member; absent or null is "". */
    static String string(Map<String, Object> object, String key) {
        Object value = object.get(key);
        if (value == null) {
            return "";
        }
        if (!(value instanceof String)) {
            throw new IllegalArgumentException(key + " must be a string");
        }
        return (String) value;
    }

    /** An integer member; absent or null is 0. */
    static long integer(Map<String, Object> object, String key) {
        Object value = object.get(key);
        if (value == null) {
            return 0;
        }
        if (!(value instanceof Long)) {
            throw new IllegalArgumentException(key + " must be an integer");
        }
        return (Long) value;
    }

    /** A boolean member; absent or null is false. */
    static boolean bool(Map<String, Object> object, String key) {
        Object value = object.get(key);
        if (value == null) {
            return false;
        }
        if (!(value instanceof Boolean)) {
            throw new IllegalArgumentException(key + " must be a boolean");
        }
        return (Boolean) value;
    }

    /** A string-array member; absent or null is null (not an empty list). */
    @SuppressWarnings("unchecked")
    static List<String> strings(Map<String, Object> object, String key) {
        Object value = object.get(key);
        if (value == null) {
            return null;
        }
        if (!(value instanceof List)) {
            throw new IllegalArgumentException(key + " must be an array");
        }
        List<String> out = new ArrayList<>();
        for (Object item : (List<Object>) value) {
            if (!(item instanceof String)) {
                throw new IllegalArgumentException(key + " must hold strings");
            }
            out.add((String) item);
        }
        return out;
    }

    private static final class Parser {
        private final String text;
        private int at;

        Parser(String text) {
            this.text = text;
        }

        void space() {
            while (at < text.length()) {
                char c = text.charAt(at);
                if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
                    at++;
                } else {
                    return;
                }
            }
        }

        private IllegalArgumentException bad(String what) {
            return new IllegalArgumentException("invalid JSON at " + at + ": " + what);
        }

        Object value() {
            if (at >= text.length()) {
                throw bad("unexpected end");
            }
            char c = text.charAt(at);
            switch (c) {
                case '{':
                    return object();
                case '[':
                    return array();
                case '"':
                    return string();
                case 't':
                    return literal("true", Boolean.TRUE);
                case 'f':
                    return literal("false", Boolean.FALSE);
                case 'n':
                    return literal("null", null);
                default:
                    return number();
            }
        }

        private Object literal(String word, Object value) {
            if (!text.startsWith(word, at)) {
                throw bad("unexpected token");
            }
            at += word.length();
            return value;
        }

        private Object number() {
            int start = at;
            if (at < text.length() && text.charAt(at) == '-') {
                at++;
            }
            int digits = at;
            while (at < text.length() && Character.isDigit(text.charAt(at)) && text.charAt(at) < 0x80) {
                at++;
            }
            if (at == digits) {
                throw bad("expected a value");
            }
            boolean integral = true;
            if (at < text.length() && text.charAt(at) == '.') {
                integral = false;
                at++;
                while (at < text.length() && text.charAt(at) >= '0' && text.charAt(at) <= '9') {
                    at++;
                }
            }
            if (at < text.length() && (text.charAt(at) == 'e' || text.charAt(at) == 'E')) {
                integral = false;
                at++;
                if (at < text.length() && (text.charAt(at) == '+' || text.charAt(at) == '-')) {
                    at++;
                }
                while (at < text.length() && text.charAt(at) >= '0' && text.charAt(at) <= '9') {
                    at++;
                }
            }
            String number = text.substring(start, at);
            if (integral) {
                try {
                    return Long.valueOf(number);
                } catch (NumberFormatException e) {
                    return Double.valueOf(number);
                }
            }
            return Double.valueOf(number);
        }

        private String string() {
            at++;
            StringBuilder out = new StringBuilder();
            while (true) {
                if (at >= text.length()) {
                    throw bad("unterminated string");
                }
                char c = text.charAt(at++);
                if (c == '"') {
                    return out.toString();
                }
                if (c < 0x20) {
                    throw bad("control character in string");
                }
                if (c != '\\') {
                    out.append(c);
                    continue;
                }
                if (at >= text.length()) {
                    throw bad("unterminated escape");
                }
                char escape = text.charAt(at++);
                switch (escape) {
                    case '"': out.append('"'); break;
                    case '\\': out.append('\\'); break;
                    case '/': out.append('/'); break;
                    case 'b': out.append('\b'); break;
                    case 'f': out.append('\f'); break;
                    case 'n': out.append('\n'); break;
                    case 'r': out.append('\r'); break;
                    case 't': out.append('\t'); break;
                    case 'u': {
                        int unit = hex4();
                        if (Character.isHighSurrogate((char) unit)) {
                            if (text.startsWith("\\u", at)) {
                                int save = at;
                                at += 2;
                                int low = hex4();
                                if (Character.isLowSurrogate((char) low)) {
                                    out.append((char) unit).append((char) low);
                                    break;
                                }
                                at = save;
                            }
                            out.append('�');
                        } else if (Character.isLowSurrogate((char) unit)) {
                            out.append('�');
                        } else {
                            out.append((char) unit);
                        }
                        break;
                    }
                    default:
                        throw bad("invalid escape");
                }
            }
        }

        private int hex4() {
            if (at + 4 > text.length()) {
                throw bad("short \\u escape");
            }
            int value = 0;
            for (int i = 0; i < 4; i++) {
                int digit = Character.digit(text.charAt(at + i), 16);
                if (digit < 0) {
                    throw bad("invalid \\u escape");
                }
                value = (value << 4) | digit;
            }
            at += 4;
            return value;
        }

        private Map<String, Object> object() {
            at++;
            Map<String, Object> out = new LinkedHashMap<>();
            space();
            if (at < text.length() && text.charAt(at) == '}') {
                at++;
                return out;
            }
            while (true) {
                space();
                if (at >= text.length() || text.charAt(at) != '"') {
                    throw bad("expected a member name");
                }
                String key = string();
                space();
                if (at >= text.length() || text.charAt(at) != ':') {
                    throw bad("expected a colon");
                }
                at++;
                space();
                out.put(key, value());
                space();
                if (at >= text.length()) {
                    throw bad("unterminated object");
                }
                char c = text.charAt(at++);
                if (c == '}') {
                    return out;
                }
                if (c != ',') {
                    throw bad("expected a comma");
                }
            }
        }

        private List<Object> array() {
            at++;
            List<Object> out = new ArrayList<>();
            space();
            if (at < text.length() && text.charAt(at) == ']') {
                at++;
                return out;
            }
            while (true) {
                space();
                out.add(value());
                space();
                if (at >= text.length()) {
                    throw bad("unterminated array");
                }
                char c = text.charAt(at++);
                if (c == ']') {
                    return out;
                }
                if (c != ',') {
                    throw bad("expected a comma");
                }
            }
        }
    }
}
