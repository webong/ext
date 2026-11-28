package io.github.webong.ext.web;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.Arrays;
import java.util.List;
import java.util.Map;

final class CookieWorkflowTests {
    private CookieWorkflowTests() {}
    static void run() throws Exception {
        String root = System.getenv("EXT_WEB_TESTDATA");
        String text = new String(Files.readAllBytes(Paths.get(root == null ? "res/web/testdata" : root, "cookie/v2/workflows.json")), StandardCharsets.UTF_8);
        for (Object raw : (List<?>) Json.parse(text)) {
            Map<String, Object> c = Json.object(raw, "case");
            String name = Json.string(c, "name");
            List<Cookie> cookies;
            try { cookies = CookieWorkflows.parse(Json.string(c, "input").getBytes(StandardCharsets.UTF_8)); }
            catch (CookieException failure) {
                WebTests.check(!Json.bool(c, "valid"), name);
                WebTests.check(!failure.toString().contains("fixture-token"), "redacted error");
                continue;
            }
            WebTests.check(Json.bool(c, "valid"), name);
            if (!Json.bool(c, "valid")) continue;
            List<?> expected = (List<?>) c.get("cookies");
            WebTests.eq(expected.size(), cookies.size(), name);
            for (int i = 0; i < expected.size(); i++) WebTests.eq(Cookie.fromObject(Json.object(expected.get(i), "cookie")), cookies.get(i), name);
            if (c.containsKey("header")) WebTests.eq(c.get("header"), CookieWorkflows.header(cookies, Json.string(c, "site")), name);
            if (Json.bool(c, "header_error")) {
                try { CookieWorkflows.header(cookies, Json.string(c, "site")); WebTests.check(false, name); }
                catch (CookieException expectedError) { WebTests.check(true, name); }
            }
            if (c.containsKey("jar")) WebTests.eq(c.get("jar"), CookieWorkflows.netscape(cookies), name);
        }
        for (byte[] invalid : new byte[][]{new byte[]{(byte) 0xff}, new byte[CookieWorkflows.MAX_INPUT_BYTES + 1], new String(new char[129]).replace('\0', '[').getBytes(StandardCharsets.UTF_8)}) {
            try { CookieWorkflows.parse(invalid); WebTests.check(false, "invalid input rejected"); }
            catch (CookieException expected) { WebTests.check(true, "invalid input rejected"); }
        }
        Cookie cookie = Cookie.builder("sid", "fixture-token", "example.com", "/").build();
        WebTests.eq("https://example.com/a", CookieWorkflows.bundle(cookie, "https://example.com/a?token=x#fragment", "").site, "bundle removes query");
        WebTests.eq(Arrays.asList(cookie), CookieWorkflows.parse(CookieWorkflows.netscape(Arrays.asList(cookie)).getBytes(StandardCharsets.UTF_8)), "jar round trip");
        try { CookieWorkflows.netscape(Arrays.asList(cookie.toBuilder().partitionKey("https://top.example").build())); WebTests.check(false, "isolation rejected"); }
        catch (CookieException expected) { WebTests.check(true, "isolation rejected"); }
    }
}
