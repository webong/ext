package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;

/** Typed mobile input; no paths or remote endpoints are accepted. */
public final class WorkspaceInput {
    public final String id, name, source, revision, url, runAt, world;
    public final List<String> matches, excludeMatches;
    public final List<UserscriptRecord> records;
    private WorkspaceInput(Map<String, Object> m, String target, String kind) {
        List<String> allowed = kind.equals("userscript")
            ? Arrays.asList("id", "name", "source", "revision", "matches", "excludeMatches", "sessionTarget")
            : Arrays.asList("target", "url", "source", "options", "records");
        for (String key : m.keySet()) if (!allowed.contains(key)) throw new IllegalArgumentException("unsupported workspace input field");
        id = Json.string(m, "id"); name = Json.string(m, "name"); source = Json.string(m, "source");
        revision = Json.string(m, "revision"); url = Json.string(m, "url");
        matches = Json.strings(m, "matches"); excludeMatches = Json.strings(m, "excludeMatches");
        Object selected = m.get(kind.equals("userscript") ? "sessionTarget" : "target");
        if (selected != null) {
            Map<String, Object> s = Json.object(selected, "target");
            for (String key : s.keySet()) if (!Arrays.asList("id", "protocol", "endpoint", "url", "title").contains(key)) throw new IllegalArgumentException("unsupported target field");
            Json.string(s,"url"); Json.string(s,"title");
            String id = Json.string(s, "id"), protocol = Json.string(s, "protocol");
            if ((!id.isEmpty() && !id.equals(target)) || (!protocol.isEmpty() && !protocol.equals("webview")) || !Json.string(s, "endpoint").isEmpty())
                throw new IllegalArgumentException("workspace target mismatch");
        }
        Map<String, Object> options = m.get("options") == null ? java.util.Collections.emptyMap() : Json.object(m.get("options"), "options");
        for (String key : options.keySet()) if (!Arrays.asList("runAt", "world").contains(key)) throw new IllegalArgumentException("unsupported injection option");
        runAt = Json.string(options, "runAt"); world = Json.string(options, "world");
        records = new ArrayList<>();
        if (m.containsKey("records")) {
            if (!(m.get("records") instanceof List)) throw new IllegalArgumentException("records must be an array");
            for (Object value : (List<?>) m.get("records")) {
                Map<String, Object> r = Json.object(value, "record");
                records.add(new UserscriptRecord(Json.string(r,"target"), Json.string(r,"id"), Json.string(r,"name"), Json.string(r,"revision"),
                    Json.strings(r,"matches"), Json.strings(r,"excludeMatches"), Json.bool(r,"enabled"), Json.string(r,"source")));
            }
        }
    }
    public static WorkspaceInput decode(BrowserManagement.Request request, String target) {
        request.validate();
        String text = request.input == null || request.input.isEmpty() ? "{}" : request.input;
        ManagementJSON.read(text, true);
        return new WorkspaceInput(Json.object(Json.parse(text), "input"), target, request.kind);
    }
    public static void validateSource(String source) {
        if (source == null || !GoText.isWellFormed(source) || source.isEmpty() || GoText.utf8Length(source) > (1 << 20))
            throw new IllegalArgumentException("invalid workspace injection source");
    }
    public void validateInjection() {
        validateSource(source);
        if ((!runAt.isEmpty() && !runAt.equals("now")) || (!world.isEmpty() && !world.equals("page")))
            throw new IllegalArgumentException("unsupported workspace injection");
    }
    public static String quote(String text) { return GoText.jsonQuote(new StringBuilder(), text).toString(); }
    public static String targetJson(String target, String url, String title) {
        return "{\"id\":" + quote(target) + ",\"protocol\":\"webview\",\"endpoint\":\"\",\"url\":" + quote(url) + ",\"title\":" + quote(title) + "}";
    }
}
