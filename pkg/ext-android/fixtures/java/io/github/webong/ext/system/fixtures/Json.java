package io.github.webong.ext.system.fixtures;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * The little JSON the fixtures need, for tests: the JDK has no parser, and Android's org.json is
 * not on a desktop JDK. Objects become {@link Map}, arrays {@link List}, integers {@link Long},
 * other numbers {@link Double}, and null stays null.
 */
public final class Json {
    private final String text;
    private int at;

    private Json(String text) {
        this.text = text;
    }

    public static Object parse(String text) {
        Json parser = new Json(text);
        parser.space();
        Object value = parser.value();
        parser.space();
        if (parser.at != text.length()) {
            throw new IllegalArgumentException("trailing data in JSON at " + parser.at);
        }
        return value;
    }

    @SuppressWarnings("unchecked")
    public static Map<String, Object> object(Object value) {
        if (!(value instanceof Map)) {
            throw new IllegalArgumentException("expected a JSON object");
        }
        return (Map<String, Object>) value;
    }

    @SuppressWarnings("unchecked")
    public static List<Object> array(Object value) {
        if (!(value instanceof List)) {
            throw new IllegalArgumentException("expected a JSON array");
        }
        return (List<Object>) value;
    }

    private void space() {
        while (at < text.length() && " \t\r\n".indexOf(text.charAt(at)) >= 0) {
            at++;
        }
    }

    private IllegalArgumentException error(String what) {
        return new IllegalArgumentException(what + " at " + at);
    }

    private Object value() {
        if (at >= text.length()) {
            throw error("unexpected end of JSON");
        }
        char c = text.charAt(at);
        if (c == '{') {
            return object();
        }
        if (c == '[') {
            return array();
        }
        if (c == '"') {
            return string();
        }
        if (text.startsWith("true", at)) {
            at += 4;
            return Boolean.TRUE;
        }
        if (text.startsWith("false", at)) {
            at += 5;
            return Boolean.FALSE;
        }
        if (text.startsWith("null", at)) {
            at += 4;
            return null;
        }
        return number();
    }

    private Map<String, Object> object() {
        Map<String, Object> out = new LinkedHashMap<>();
        at++;
        space();
        if (text.charAt(at) == '}') {
            at++;
            return out;
        }
        while (true) {
            space();
            if (at >= text.length() || text.charAt(at) != '"') {
                throw error("expected a key");
            }
            String key = string();
            space();
            if (at >= text.length() || text.charAt(at) != ':') {
                throw error("expected a colon");
            }
            at++;
            space();
            out.put(key, value());
            space();
            char c = at < text.length() ? text.charAt(at++) : '\0';
            if (c == '}') {
                return out;
            }
            if (c != ',') {
                throw error("expected a comma or a brace");
            }
        }
    }

    private List<Object> array() {
        List<Object> out = new ArrayList<>();
        at++;
        space();
        if (text.charAt(at) == ']') {
            at++;
            return out;
        }
        while (true) {
            space();
            out.add(value());
            space();
            char c = at < text.length() ? text.charAt(at++) : '\0';
            if (c == ']') {
                return out;
            }
            if (c != ',') {
                throw error("expected a comma or a bracket");
            }
        }
    }

    private String string() {
        StringBuilder out = new StringBuilder();
        at++;
        while (true) {
            if (at >= text.length()) {
                throw error("unterminated string");
            }
            char c = text.charAt(at++);
            if (c == '"') {
                return out.toString();
            }
            if (c != '\\') {
                out.append(c);
                continue;
            }
            char e = text.charAt(at++);
            switch (e) {
                case '"': case '\\': case '/': out.append(e); break;
                case 'b': out.append('\b'); break;
                case 'f': out.append('\f'); break;
                case 'n': out.append('\n'); break;
                case 'r': out.append('\r'); break;
                case 't': out.append('\t'); break;
                case 'u':
                    out.append((char) Integer.parseInt(text.substring(at, at + 4), 16));
                    at += 4;
                    break;
                default: throw error("bad escape");
            }
        }
    }

    private Object number() {
        int start = at;
        while (at < text.length() && "+-0123456789.eE".indexOf(text.charAt(at)) >= 0) {
            at++;
        }
        String digits = text.substring(start, at);
        if (digits.isEmpty()) {
            throw error("unexpected character");
        }
        if (digits.indexOf('.') >= 0 || digits.indexOf('e') >= 0 || digits.indexOf('E') >= 0) {
            return Double.valueOf(digits);
        }
        return Long.valueOf(digits);
    }
}
