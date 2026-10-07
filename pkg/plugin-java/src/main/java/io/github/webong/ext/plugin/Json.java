package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * The little JSON this SDK needs. The engine owns strict validation; this reads
 * input the engine already accepted, so it stays small. Numbers stay as text.
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

    /** Parses one JSON value. Objects become Map, arrays List, numbers raw String text. */
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
        Parser parser = new Parser(json);
        parser.space();
        if (parser.i >= json.length || json[parser.i] != '{') {
            return new byte[0];
        }
        parser.i++;
        while (true) {
            parser.space();
            if (parser.i >= json.length || json[parser.i] != '"') {
                return new byte[0];
            }
            String key = parser.string();
            parser.space();
            if (parser.i >= json.length || json[parser.i] != ':') {
                return new byte[0];
            }
            parser.i++;
            parser.space();
            int start = parser.i;
            parser.value();
            if (key.equals("payload")) {
                return java.util.Arrays.copyOfRange(json, start, parser.i);
            }
            parser.space();
            if (parser.i >= json.length || json[parser.i] != ',') {
                return new byte[0];
            }
            parser.i++;
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
                        int start = i;
                        while (i < in.length && " \t\r\n,]}".indexOf(in[i]) < 0) {
                            i++;
                        }
                        String literal = new String(in, start, i - start, StandardCharsets.US_ASCII);
                        return literal.equals("null") ? NULL : literal;
                    }
                }
            } finally {
                depth--;
            }
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
