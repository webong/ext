package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * The little JSON this SDK needs. The engine owns strict validation; this reads
 * input the engine already accepted, so it stays small. Numbers stay as text, in a
 * {@link Num}, so a number can be told from a string.
 */
final class Json {
    private Json() {}

    static StringBuilder quote(StringBuilder out, String text) {
        out.append('"');
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            switch (c) {
                case '"' -> out.append("\\\"");
                case '\\' -> out.append("\\\\");
                case '\n' -> out.append("\\n");
                case '\r' -> out.append("\\r");
                case '\t' -> out.append("\\t");
                default -> {
                    if (c < 0x20) {
                        out.append(String.format("\\u%04x", (int) c));
                    } else {
                        out.append(c);
                    }
                }
            }
        }
        return out.append('"');
    }

    /** A JSON number, as the text it was written in. */
    record Num(String text) {}

    /** Parses one JSON value. Objects become Map, arrays List, numbers {@link Num}, true and false Boolean. */
    static Object parse(byte[] json) {
        Parser parser = new Parser(json);
        Object value = parser.value();
        parser.space();
        if (parser.i != json.length) {
            throw new IllegalArgumentException("trailing data");
        }
        return value;
    }

    @SuppressWarnings("unchecked")
    static Map<String, Object> object(Object value) {
        return value instanceof Map ? (Map<String, Object>) value : null;
    }

    static String string(Object value) {
        return value instanceof String s ? s : null;
    }

    /** Marks an absent value in a parsed object that is not a JSON null. */
    static final Object NULL = new Object();

    /**
     * The bytes of the top-level "payload" value of an already validated request,
     * without re-encoding them. Empty when the key is absent.
     */
    static byte[] rawPayload(byte[] json) {
        byte[] payload = member(json, "payload");
        return payload == null ? new byte[0] : payload;
    }

    /**
     * The bytes of a top-level member of an object, without re-encoding them, or null when
     * the member is absent or the text is not an object. It skips the other members without
     * building them, so it suits finding out what kind of frame has just arrived.
     */
    static byte[] member(byte[] json, String name) {
        try {
            Parser parser = new Parser(json);
            parser.space();
            if (parser.i >= json.length || json[parser.i] != '{') {
                return null;
            }
            parser.i++;
            parser.space();
            if (parser.i < json.length && json[parser.i] == '}') {
                return null;
            }
            while (true) {
                parser.space();
                String key = parser.string();
                parser.space();
                parser.expect(':');
                parser.space();
                int start = parser.i;
                parser.skip();
                if (key.equals(name)) {
                    return java.util.Arrays.copyOfRange(json, start, parser.i);
                }
                parser.space();
                if (parser.i >= json.length || json[parser.i] != ',') {
                    return null;
                }
                parser.i++;
            }
        } catch (RuntimeException e) {
            return null;
        }
    }

    private static final class Parser {
        private final byte[] in;
        int i;
        private int depth;

        Parser(byte[] in) {
            this.in = in;
        }

        void space() {
            while (i < in.length && (in[i] == ' ' || in[i] == '\t' || in[i] == '\n' || in[i] == '\r')) {
                i++;
            }
        }

        Object value() {
            space();
            if (i >= in.length) {
                throw new IllegalArgumentException("unexpected end");
            }
            if (++depth > 64) {
                throw new IllegalArgumentException("too deep");
            }
            try {
                switch (in[i]) {
                    case '{': {
                        i++;
                        Map<String, Object> map = new LinkedHashMap<>();
                        space();
                        if (in[i] == '}') {
                            i++;
                            return map;
                        }
                        while (true) {
                            space();
                            String key = string();
                            space();
                            expect(':');
                            map.put(key, value());
                            space();
                            if (in[i] == ',') {
                                i++;
                            } else {
                                expect('}');
                                return map;
                            }
                        }
                    }
                    case '[': {
                        i++;
                        List<Object> list = new ArrayList<>();
                        space();
                        if (in[i] == ']') {
                            i++;
                            return list;
                        }
                        while (true) {
                            list.add(value());
                            space();
                            if (in[i] == ',') {
                                i++;
                            } else {
                                expect(']');
                                return list;
                            }
                        }
                    }
                    case '"':
                        return string();
                    default: {
                        String literal = literal();
                        return switch (literal) {
                            case "null" -> NULL;
                            case "true" -> Boolean.TRUE;
                            case "false" -> Boolean.FALSE;
                            default -> new Num(literal);
                        };
                    }
                }
            } finally {
                depth--;
            }
        }

        String literal() {
            int start = i;
            while (i < in.length && " \t\r\n,]}".indexOf(in[i]) < 0) {
                i++;
            }
            return new String(in, start, i - start, StandardCharsets.US_ASCII);
        }

        /** Steps over one value without building it. */
        void skip() {
            space();
            if (i >= in.length) {
                throw new IllegalArgumentException("unexpected end");
            }
            int depth = 0;
            do {
                space();
                if (i >= in.length) {
                    throw new IllegalArgumentException("unexpected end");
                }
                switch (in[i]) {
                    case '{', '[' -> {
                        if (++depth > 64) {
                            throw new IllegalArgumentException("too deep");
                        }
                        i++;
                    }
                    case '}', ']' -> {
                        depth--;
                        i++;
                    }
                    case ',', ':' -> i++;
                    case '"' -> string();
                    default -> literal();
                }
            } while (depth > 0);
        }

        void expect(char c) {
            if (i >= in.length || in[i] != c) {
                throw new IllegalArgumentException("expected " + c);
            }
            i++;
        }

        String string() {
            expect('"');
            StringBuilder out = new StringBuilder();
            int start = i;
            while (true) {
                if (i >= in.length) {
                    throw new IllegalArgumentException("unterminated string");
                }
                byte b = in[i];
                if (b == '"') {
                    out.append(new String(in, start, i - start, StandardCharsets.UTF_8));
                    i++;
                    return out.toString();
                }
                if (b == '\\') {
                    out.append(new String(in, start, i - start, StandardCharsets.UTF_8));
                    i++;
                    char e = (char) in[i++];
                    switch (e) {
                        case 'b' -> out.append('\b');
                        case 'f' -> out.append('\f');
                        case 'n' -> out.append('\n');
                        case 'r' -> out.append('\r');
                        case 't' -> out.append('\t');
                        case 'u' -> {
                            out.append((char) Integer.parseInt(new String(in, i, 4, StandardCharsets.US_ASCII), 16));
                            i += 4;
                        }
                        default -> out.append(e);
                    }
                    start = i;
                    continue;
                }
                i++;
            }
        }
    }
}
