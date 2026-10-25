package io.github.webong.ext.web;

import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

/** Portable import/export. Outputs contain credentials. Query provenance is not returned by parse. */
public final class CookieWorkflows {
    public static final int MAX_INPUT_BYTES = 8 << 20;
    private CookieWorkflows() {}
    private static CookieException invalid() { return new CookieException("invalid or unsupported cookie input"); }

    public static void requirePortableScope(Cookie cookie) throws CookieException {
        CookieRules.validate(cookie);
        if (!cookie.partitionKey.isEmpty() || cookie.hasCrossSiteAncestor || !cookie.attributes.isEmpty())
            throw new CookieException("native cookie isolation cannot be represented in this format");
    }
    public static void requireHeaderValue(String value) throws CookieException {
        for (int i = 0; i < value.length(); i++) {
            int b = value.charAt(i);
            if (!(b == 0x21 || b >= 0x23 && b <= 0x2b || b >= 0x2d && b <= 0x3a
                    || b >= 0x3c && b <= 0x5b || b >= 0x5d && b <= 0x7e))
                throw new CookieException("cookie value cannot be represented in an HTTP header");
        }
    }
    public static String header(List<Cookie> cookies, String site) throws CookieException, SiteException {
        Site url = Site.parse(site);
        List<Cookie> selected = new ArrayList<>();
        for (Cookie cookie : cookies) {
            CookieRules.validate(cookie);
            if (!CookieRules.matches(url, cookie, false)) continue;
            requirePortableScope(cookie);
            requireHeaderValue(cookie.value);
            selected.add(cookie);
        }
        selected.sort((a, b) -> Integer.compare(b.path.getBytes(StandardCharsets.UTF_8).length, a.path.getBytes(StandardCharsets.UTF_8).length));
        List<String> parts = new ArrayList<>();
        for (Cookie cookie : selected) parts.add(cookie.name + "=" + cookie.value);
        return String.join("; ", parts);
    }
    /** SameSite and provenance cannot be retained by the Netscape format. */
    public static String netscape(List<Cookie> cookies) throws CookieException {
        StringBuilder out = new StringBuilder("# Netscape HTTP Cookie File\n# Exported by CTX; SameSite and source metadata are not represented.\n");
        for (Cookie cookie : cookies) {
            requirePortableScope(cookie);
            if (cookie.value.indexOf('\t') >= 0 || cookie.value.indexOf('\r') >= 0 || cookie.value.indexOf('\n') >= 0) throw invalid();
            out.append(cookie.httpOnly ? "#HttpOnly_" : "").append(cookie.domain).append('\t')
                .append(cookie.domain.startsWith(".") ? "TRUE" : "FALSE").append('\t').append(cookie.path).append('\t')
                .append(cookie.secure ? "TRUE" : "FALSE").append('\t').append(cookie.expiry < 0 ? 1 : cookie.expiry)
                .append('\t').append(cookie.name).append('\t').append(cookie.value).append('\n');
        }
        return out.toString();
    }
    public static CookieBundle bundle(Cookie cookie, String site, String source) throws CookieException, SiteException {
        String clean = Site.parse(site).toString().split("[?#]", 2)[0];
        CookieBundle result = new CookieBundle(2, source == null || source.isEmpty() ? "inline" : source, clean, cookie);
        CookieRules.validate(result);
        return result;
    }
    public static List<Cookie> parse(byte[] data) throws CookieException {
        if (data.length > MAX_INPUT_BYTES) throw invalid();
        String text;
        try { text = StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(data)).toString(); }
        catch (CharacterCodingException failure) { throw invalid(); }
        if (text.startsWith("\ufeff")) text = text.substring(1);
        String trimmed = GoText.trimSpace(text);
        if (trimmed.isEmpty()) throw invalid();
        if (!trimmed.startsWith("[") && !trimmed.startsWith("{")) return parseNetscape(text);
        try {
            checkDepth(trimmed);
            Object json = Json.parse(trimmed);
            List<?> rows;
            if (json instanceof List) rows = (List<?>) json;
            else {
                Map<String, Object> object = Json.object(json, "input");
                if (object.containsKey("cookie") && object.containsKey("cookies")) throw invalid();
                if (object.containsKey("cookie")) {
                    if (Json.integer(object, "version") != 2) throw invalid();
                    Json.string(object, "source");
                    rows = Arrays.asList(object.get("cookie"));
                } else if (object.get("cookies") instanceof List) rows = (List<?>) object.get("cookies");
                else throw invalid();
            }
            List<Cookie> result = new ArrayList<>();
            for (Object row : rows) result.add(parseObject(row));
            return result;
        } catch (IllegalArgumentException failure) { throw invalid(); }
    }
    private static void checkDepth(String text) throws CookieException {
        int depth = 0;
        boolean quoted = false, escaped = false;
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (quoted) {
                if (escaped) escaped = false;
                else if (c == '\\') escaped = true;
                else if (c == '"') quoted = false;
            } else if (c == '"') quoted = true;
            else if (c == '{' || c == '[') { if (++depth > 128) throw invalid(); }
            else if (c == '}' || c == ']') depth--;
        }
    }
    private static Cookie parseObject(Object raw) throws CookieException {
        Map<String, Object> o = new LinkedHashMap<>(Json.object(raw, "cookie"));
        Set<String> allowed = new HashSet<>(Arrays.asList(("id ref name value domain path expiry secure http_only same_site_policy partition_key has_cross_site_ancestor attributes source source_info hostOnly httpOnly expirationDate expires session sameSite storeId firstPartyDomain partitionKey").split(" ")));
        if (!allowed.containsAll(o.keySet())) throw invalid();
        for (String key : Arrays.asList("name", "value", "domain", "path")) if (!(o.get(key) instanceof String)) throw invalid();
        for (String key : Arrays.asList("storeId", "firstPartyDomain")) if (o.get(key) != null && !"".equals(o.get(key))) throw invalid();
        if (o.get("partitionKey") != null) throw invalid();
        for (String[] pair : new String[][]{{"httpOnly", "http_only"}, {"sameSite", "same_site_policy"}}) {
            if (o.containsKey(pair[0])) { if (o.containsKey(pair[1])) throw invalid(); o.put(pair[1], o.get(pair[0])); }
        }
        for (String key : Arrays.asList("secure", "http_only", "has_cross_site_ancestor", "same_site_policy", "partition_key"))
            if (o.containsKey(key) && o.get(key) == null) throw invalid();
        boolean seen = false;
        for (String key : Arrays.asList("expiry", "expirationDate", "expires")) {
            if (!o.containsKey(key)) continue;
            if (seen || !(o.get(key) instanceof Number)) throw invalid();
            seen = true;
            double seconds = ((Number) o.get(key)).doubleValue();
            if (!Double.isFinite(seconds) || seconds < -1 || seconds > 253402300799L) throw invalid();
            if (key.equals("expires") && seconds == -1) seconds = 0;
            o.put("expiry", seconds > 0 && seconds < 1 ? -1L : (long) Math.floor(seconds));
        }
        if (o.containsKey("hostOnly")) {
            if (!(o.get("hostOnly") instanceof Boolean)) throw invalid();
            String domain = (String) o.get("domain");
            if (domain.startsWith(".")) domain = domain.substring(1);
            o.put("domain", ((Boolean) o.get("hostOnly") ? "" : ".") + domain);
        }
        Cookie cookie = Cookie.fromObject(o);
        if (o.containsKey("session") && (!(o.get("session") instanceof Boolean) || (Boolean) o.get("session") != (cookie.expiry == 0))) throw invalid();
        String policy = cookie.sameSitePolicy.toLowerCase(Locale.ROOT);
        cookie = cookie.toBuilder().sameSitePolicy(policy.equals("no_restriction") ? "none" : policy).build();
        CookieRules.validate(cookie);
        return cookie;
    }
    private static List<Cookie> parseNetscape(String text) throws CookieException {
        List<Cookie> result = new ArrayList<>();
        for (String raw : text.split("\n", -1)) {
            String line = raw.endsWith("\r") ? raw.substring(0, raw.length() - 1) : raw;
            boolean httpOnly = line.startsWith("#HttpOnly_");
            if (line.isEmpty() || line.startsWith("#") && !httpOnly) continue;
            if (httpOnly) line = line.substring(10);
            String[] f = line.split("\t", -1);
            if (f.length != 7 || !(f[1].equals("TRUE") || f[1].equals("FALSE")) || !(f[3].equals("TRUE") || f[3].equals("FALSE"))) throw invalid();
            long expiry;
            try { if (!f[4].matches("[+-]?[0-9]+")) throw invalid(); expiry = Long.parseLong(f[4]); } catch (NumberFormatException failure) { throw invalid(); }
            if (expiry < 0) throw invalid();
            String domain = f[0].startsWith(".") ? f[0].substring(1) : f[0];
            if (f[1].equals("TRUE")) domain = "." + domain;
            Cookie cookie = Cookie.builder(f[5], f[6], domain, f[2]).expiry(expiry).secure(f[3].equals("TRUE")).httpOnly(httpOnly).build();
            CookieRules.validate(cookie);
            result.add(cookie);
        }
        return result;
    }
}
