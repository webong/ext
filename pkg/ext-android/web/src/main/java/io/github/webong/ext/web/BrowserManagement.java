package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.net.InetAddress;

/** Portable browser-management protocol. The caller supplies the native backend. */
public final class BrowserManagement {
    private BrowserManagement() {}
    public static final String VERSION = "1.0";
    public static final int MAX_REQUEST_INPUT_BYTES = 8 << 20;
    public static final int MAX_RESULT_BYTES = 16 << 20;
    public static final int MAX_RESPONSE_BYTES = MAX_RESULT_BYTES + (64 << 10);
    public static final int MAX_ENDPOINT_BYTES = 2048;
    public static final int MAX_ENDPOINT_RESULT_BYTES = 4096;
    public static final class Status {
        private Status() {}
        public static final String READY = "ready";
        public static final String PREPARED = "prepared";
        public static final String PACKAGED = "packaged";
        public static final String STAGED = "staged";
        public static final String SIGNED = "signed";
        public static final String INSTALLED = "installed";
        public static final String ACTIVATED = "activated";
        public static final String UPDATED = "updated";
        public static final String UNINSTALLED = "uninstalled";
        public static final String REQUESTED = "requested";
        public static final String REQUEST_REMOVED = "request-removed";
        public static final String POLICY_UPDATED = "policy-updated";
        public static final String AWAITING_BROWSER_ACTION = "awaiting-browser-action";
        public static final String AWAITING_BROWSER_CONFIRMATION = "awaiting-browser-confirmation";
        public static final String RECONCILED = "reconciled";
        public static final String CONNECTED = "connected";
        public static final String NAVIGATED = "navigated";
        public static final String INJECTED = "injected";
    }
    public static boolean persistent(String status) { return Status.INSTALLED.equals(status); }
    public static Map<String, List<String>> operations() {
        Map<String, List<String>> result = new LinkedHashMap<>();
        result.put("extension", new ArrayList<>(Arrays.asList("activate", "capabilities", "convert", "install", "package", "policy", "prepare", "sign", "stage", "store_install", "store_remove", "targets", "update_manifest")));
        result.put("userscript", new ArrayList<>(Arrays.asList("activate", "describe", "disable", "enable", "install", "list", "prepare", "uninstall", "update")));
        result.put("session", new ArrayList<>(Arrays.asList("connect", "endpoint", "inject", "navigate", "replay", "targets")));
        result.put("bookmarklet", new ArrayList<>(Arrays.asList("decode", "encode", "install_page")));
        return result;
    }
    private static int size(String value) { return value == null ? 0 : GoText.utf8Length(value); }
    private static IllegalArgumentException bad(String reason) { return new IllegalArgumentException("browser management: " + reason); }
    private static String q(String value) { StringBuilder out = new StringBuilder(); GoText.jsonQuote(out, value); return out.toString(); }
    private static String field(Map<String, String> m, String name) {
        String value = null;
        for (Map.Entry<String, String> e : m.entrySet()) if (e.getKey().equalsIgnoreCase(name)) value = e.getValue();
        return ManagementJSON.string(value);
    }
    private static String raw(Map<String, String> m, String name) {
        String value = null;
        for (Map.Entry<String, String> e : m.entrySet()) if (e.getKey().equalsIgnoreCase(name)) value = e.getValue();
        return value;
    }
    /** Raw JSON payloads are retained verbatim; null means absent, "null" is explicit JSON null. */
    public static final class Request {
        public final String version, kind, action, input;
        public Request(String kind, String action, String input) { this(VERSION, kind, action, input); }
        public Request(String version, String kind, String action, String input) {
            this.version = version; this.kind = kind; this.action = action; this.input = input;
        }
        public void validate() {
            if (!VERSION.equals(version)) throw bad("unsupported version");
            List<String> actions = operations().get(kind);
            if (actions == null || !actions.contains(action)) throw bad("unsupported operation");
            if (size(input) > MAX_REQUEST_INPUT_BYTES) throw bad("input exceeds bound");
            if (input != null && !input.isEmpty()) ManagementJSON.read(input, true);
        }
        public String toJson() {
            validate();
            return "{\"version\":" + q(version) + ",\"kind\":" + q(kind) + ",\"action\":" + q(action)
                + (input == null || input.isEmpty() ? "" : ",\"input\":" + input) + "}";
        }
        public static Request fromJson(String json) {
            if (size(json) > MAX_REQUEST_INPUT_BYTES + (64 << 10)) throw bad("request exceeds bound");
            Map<String, String> m = ManagementJSON.read(json, true);
            Request r = new Request(field(m, "version"), field(m, "kind"), field(m, "action"), raw(m, "input"));
            r.validate(); return r;
        }
        @Override public String toString() { return "BrowserManagement.Request(<redacted>)"; }
    }
    public static final class Response {
        public final String version, kind, action, status, result;
        public Response(Request request, String status, String result) { this(VERSION, request.kind, request.action, status, result); }
        public Response(String version, String kind, String action, String status, String result) {
            this.version = version; this.kind = kind; this.action = action; this.status = status; this.result = result;
        }
        public void validate(Request request) {
            if (!VERSION.equals(version) || !request.kind.equals(kind) || !request.action.equals(action)) throw bad("response does not match request");
            if (status == null || status.isEmpty() || size(status) > 64) throw bad("invalid status");
            if (size(result) > MAX_RESULT_BYTES) throw bad("result exceeds bound");
            if (result != null && !result.isEmpty()) ManagementJSON.read(result, false);
            if ("session".equals(kind) && "endpoint".equals(action)) {
                if (size(result) > MAX_ENDPOINT_RESULT_BYTES || result == null) throw bad("invalid endpoint result");
                Endpoint.fromJson(result).validate();
            }
            if ("extension".equals(kind) && (("activate".equals(action) && Status.INSTALLED.equals(status))
                || ("install".equals(action) && Status.ACTIVATED.equals(status)))) throw bad("installation and activation are distinct");
        }
        public String toJson(Request request) {
            validate(request);
            String json = "{\"version\":" + q(version) + ",\"kind\":" + q(kind) + ",\"action\":" + q(action)
                + ",\"status\":" + q(status) + (result == null || result.isEmpty() ? "" : ",\"result\":" + result) + "}";
            if (size(json) > MAX_RESPONSE_BYTES) throw bad("response exceeds bound");
            return json;
        }
        public static Response fromJson(String json, Request request) {
            if (size(json) > MAX_RESPONSE_BYTES) throw bad("response exceeds bound");
            Map<String, String> m = ManagementJSON.read(json, true);
            Response r = new Response(field(m, "version"), field(m, "kind"), field(m, "action"), field(m, "status"), raw(m, "result"));
            r.validate(request); return r;
        }
        @Override public String toString() { return "BrowserManagement.Response(<redacted>)"; }
    }
    public static final class Endpoint {
        public final String protocol, endpoint;
        public Endpoint(String protocol, String endpoint) { this.protocol = protocol; this.endpoint = endpoint; }
        public void validate() {
            if (!("cdp".equals(protocol) || "bidi".equals(protocol))) throw bad("invalid endpoint protocol");
            if (endpoint == null || !GoText.isWellFormed(endpoint) || endpoint.isEmpty() || size(endpoint) > MAX_ENDPOINT_BYTES) throw bad("invalid endpoint URL");
            try {
                if (!(endpoint.startsWith("ws://") || endpoint.startsWith("wss://"))
                    || endpoint.indexOf('?') >= 0 || endpoint.indexOf('#') >= 0) throw bad("invalid endpoint URL");
                for (int i = 0; i < endpoint.length(); i++) {
                    char c = endpoint.charAt(i);
                    if (c < 32 || c == 127) throw bad("invalid endpoint URL");
                    if (c == '%') {
                        if (i + 2 >= endpoint.length() || !endpoint.substring(i + 1, i + 3).matches("[0-9a-fA-F]{2}")) throw bad("invalid endpoint URL");
                        i += 2;
                    }
                }
                String authority = endpoint.substring(endpoint.indexOf("://") + 3).split("/", -1)[0];
                int colon = authority.lastIndexOf(':');
                if (colon < 1 || !authority.substring(colon + 1).matches("[0-9]+")) throw bad("invalid endpoint URL");
                String host = authority.substring(0, colon);
                if (host.startsWith("[") && host.endsWith("]")) {
                    host = host.substring(1, host.length() - 1);
                    if (!host.contains(":")) throw bad("invalid endpoint URL");
                }
                boolean loopback = "localhost".equalsIgnoreCase(host);
                // Literal validation before InetAddress prevents DNS resolution.
                if (!loopback && IPLiteral.isIP(host)) loopback = InetAddress.getByName(host).isLoopbackAddress();
                if (!loopback) throw bad("endpoint must be loopback");
            } catch (Exception e) { throw bad("invalid endpoint URL"); }
        }
        public String toJson() { validate(); return "{\"protocol\":" + q(protocol) + ",\"endpoint\":" + q(endpoint) + "}"; }
        public static Endpoint fromJson(String json) {
            if (size(json) > MAX_ENDPOINT_RESULT_BYTES) throw bad("endpoint result exceeds bound");
            Map<String, String> m = ManagementJSON.read(json, true);
            for (String key : m.keySet()) if (!key.equalsIgnoreCase("protocol") && !key.equalsIgnoreCase("endpoint")) throw bad("unknown endpoint field");
            Endpoint e = new Endpoint(field(m, "protocol"), field(m, "endpoint")); e.validate(); return e;
        }
        @Override public String toString() { return "BrowserManagement.Endpoint(<redacted>)"; }
    }
    @FunctionalInterface public interface Backend {
        Response manage(String profile, Request request) throws Exception;
    }
    /** Validation precedes side effects. Backend errors propagate unchanged. */
    public static Response dispatch(String profile, Request request, Backend backend) throws Exception {
        if (backend == null) throw bad("backend is required");
        request.validate();
        Response response = backend.manage(profile, request);
        if (response == null) throw bad("backend returned no response");
        response.validate(request); return response;
    }
}
