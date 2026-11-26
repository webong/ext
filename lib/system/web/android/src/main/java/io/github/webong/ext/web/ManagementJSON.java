package io.github.webong.ext.web;

import java.util.LinkedHashMap;
import java.util.Map;

/** Syntax-only JSON reader: preserves arbitrary numbers and raw operation payloads. */
final class ManagementJSON {
    private final String text;
    private int at;
    private final Map<String, String> members = new LinkedHashMap<>();
    private ManagementJSON(String text) { this.text = text; }
    static Map<String, String> read(String text, boolean object) {
        if (!GoText.isWellFormed(text)) throw bad();
        ManagementJSON p = new ManagementJSON(text);
        p.space();
        if (object && (p.at == text.length() || text.charAt(p.at) != '{')) throw bad();
        p.value(0);
        p.space();
        if (p.at != text.length()) throw bad();
        return p.members;
    }
    static String string(String raw) {
        if (raw == null || raw.equals("null")) return "";
        Object value = Json.parse(raw);
        if (!(value instanceof String)) throw bad();
        return (String) value;
    }
    private static String canonical(String key) {
        for (String name : new String[]{"version", "kind", "action", "input", "status", "result", "protocol", "endpoint"})
            if (name.equalsIgnoreCase(key)) return name;
        return key;
    }
    private static IllegalArgumentException bad() { return new IllegalArgumentException("invalid management JSON"); }
    private void space() { while (at < text.length() && " \t\r\n".indexOf(text.charAt(at)) >= 0) at++; }
    private boolean take(char c) { if (at < text.length() && text.charAt(at) == c) { at++; return true; } return false; }
    private void need(char c) { if (!take(c)) throw bad(); }
    private boolean digit() { return at < text.length() && text.charAt(at) >= '0' && text.charAt(at) <= '9'; }
    private void digits() { if (!digit()) throw bad(); while (digit()) at++; }
    private String quoted() {
        int start = at;
        need('"');
        while (at < text.length()) {
            char c = text.charAt(at++);
            if (c == '"') return text.substring(start, at);
            if (c < 32) throw bad();
            if (c == '\\') {
                if (at == text.length()) throw bad();
                char e = text.charAt(at++);
                if (e == 'u') {
                    for (int n = 0; n < 4; n++) {
                        if (at == text.length() || "0123456789abcdefABCDEF".indexOf(text.charAt(at++)) < 0) throw bad();
                    }
                } else if ("\"\\/bfnrt".indexOf(e) < 0) throw bad();
            }
        }
        throw bad();
    }
    private void value(int depth) {
        space();
        if (at == text.length()) throw bad();
        char c = text.charAt(at);
        if (c == '{' || c == '[') {
            if (depth >= 128) throw bad();
            at++;
            char end = c == '{' ? '}' : ']';
            space();
            if (take(end)) return;
            do {
                space();
                String key = null;
                if (c == '{') { key = string(quoted()); space(); need(':'); space(); }
                int start = at;
                value(depth + 1);
                if (depth == 0 && key != null) members.put(canonical(key), text.substring(start, at));
                space();
                if (take(end)) return;
                need(',');
            } while (true);
        }
        if (c == '"') { quoted(); return; }
        for (String literal : new String[]{"true", "false", "null"}) {
            if (text.startsWith(literal, at)) { at += literal.length(); return; }
        }
        take('-');
        if (!take('0')) digits();
        if (take('.')) digits();
        if (take('e') || take('E')) { if (!take('+')) take('-'); digits(); }
    }
}
