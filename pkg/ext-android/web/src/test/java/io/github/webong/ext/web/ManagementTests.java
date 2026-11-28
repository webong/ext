package io.github.webong.ext.web;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.List;
import java.util.Map;

final class ManagementTests {
    private ManagementTests() {}
    private interface Action { void run() throws Exception; }
    private static void rejects(Action action, String label) throws Exception {
        try { action.run(); WebTests.check(false, label); }
        catch (IllegalArgumentException expected) { WebTests.check(true, label); }
    }
    private static String repeat(char c, int n) { char[] chars = new char[n]; java.util.Arrays.fill(chars, c); return new String(chars); }
    static void run() throws Exception {
        String root = System.getenv("EXT_WEB_TESTDATA");
        Map<String, Object> f = Json.object(Json.parse(new String(Files.readAllBytes(Paths.get(root == null ? "res/web/testdata" : root, "management/v1/protocol.json")), StandardCharsets.UTF_8)), "fixture");
        WebTests.eq(f.get("operations"), BrowserManagement.operations(), "operation vocabulary");
        for (Object status : (List<?>) f.get("statuses")) WebTests.eq("installed".equals(status), BrowserManagement.persistent((String) status), "persistence");
        for (Object raw : (List<?>) f.get("cases")) {
            Map<String, Object> c = Json.object(raw, "case");
            String name = Json.string(c, "name");
            BrowserManagement.Request request;
            try { request = BrowserManagement.Request.fromJson(Json.string(c, "request")); }
            catch (IllegalArgumentException e) { WebTests.check(!Json.bool(c, "valid"), name); continue; }
            WebTests.check(Json.bool(c, "valid"), name);
            WebTests.eq(request.input, BrowserManagement.Request.fromJson(request.toJson()).input, name + " raw input");
            if (c.containsKey("response")) {
                try {
                    BrowserManagement.Response response = BrowserManagement.Response.fromJson(Json.string(c, "response"), request);
                    WebTests.check(Json.bool(c, "response_valid"), name);
                    WebTests.eq(response.result, BrowserManagement.Response.fromJson(response.toJson(request), request).result, name + " raw result");
                } catch (IllegalArgumentException e) { WebTests.check(!Json.bool(c, "response_valid"), name); }
            }
        }
        boolean[] called = {false};
        rejects(() -> BrowserManagement.dispatch("p", new BrowserManagement.Request("bad", "list", null), (p, r) -> {
            called[0] = true; return new BrowserManagement.Response(r, "ready", null);
        }), "invalid request before backend");
        WebTests.check(!called[0], "backend not called");
        BrowserManagement.Request request = new BrowserManagement.Request("userscript", "list", "{\"n\":1e999}");
        BrowserManagement.Response response = BrowserManagement.dispatch("profile", request, (p, r) -> {
            WebTests.eq("profile", p, "profile forwarded"); WebTests.eq(request.input, r.input, "input forwarded");
            return new BrowserManagement.Response(r, "future", "[1e999]");
        });
        WebTests.eq("[1e999]", response.result, "result preserved");
        rejects(() -> BrowserManagement.dispatch("p", request, (p, r) -> new BrowserManagement.Response(r, "", null)), "invalid backend response");
        rejects(() -> BrowserManagement.dispatch("p", request, null), "missing backend");
        Exception failure = new Exception("backend failure");
        try { BrowserManagement.dispatch("p", request, (p, r) -> { throw failure; }); WebTests.check(false, "backend error"); }
        catch (Exception e) { WebTests.check(e == failure, "backend error propagated"); }
        rejects(() -> new BrowserManagement.Response(request, repeat((char) 0xd800, 22), null).validate(request), "surrogate status byte limit");
        rejects(() -> new BrowserManagement.Request("userscript", "list", "{\"x\":\"" + (char) 0xd800 + "\"}").validate(), "raw malformed UTF16 rejected");
        String input = "{\"x\":\"" + repeat('a', BrowserManagement.MAX_REQUEST_INPUT_BYTES - 8) + "\"}";
        new BrowserManagement.Request("userscript", "list", input).validate();
        rejects(() -> new BrowserManagement.Request("userscript", "list", input + " ").validate(), "input bound");
        String result = "\"" + repeat('a', BrowserManagement.MAX_RESULT_BYTES - 2) + "\"";
        new BrowserManagement.Response(request, "ready", result).validate(request);
        rejects(() -> new BrowserManagement.Response(request, "ready", result + " ").validate(request), "result bound");
        rejects(() -> BrowserManagement.Response.fromJson(repeat(' ', BrowserManagement.MAX_RESPONSE_BYTES + 1), request), "response bound");
        rejects(() -> new BrowserManagement.Response(request, "ready", repeat('[', 129) + "0" + repeat(']', 129)).validate(request), "depth bound");
        BrowserManagement.Endpoint endpoint = new BrowserManagement.Endpoint("cdp", "ws://localhost:9/secret-token");
        rejects(() -> new BrowserManagement.Endpoint("cdp", "ws://localhost:9/" + (char) 0xd800).validate(), "malformed endpoint string rejected");
        WebTests.check(!endpoint.toString().contains("secret-token"), "endpoint redacted");
        WebTests.check(endpoint.toJson().contains("secret-token"), "wire endpoint preserved");
        WebTests.check(!new BrowserManagement.Response(request, "ready", "\"secret-token\"").toString().contains("secret-token"), "response redacted");
    }
}
