package io.github.webong.ext.web;

import java.util.Map;
import java.util.Objects;

/** A cookie scoped to one site, as one adapter hands it to another. */
public final class CookieBundle {
    /** The browser share request and bundle wire version. */
    public static final int VERSION = 2;

    public final int version;
    public final String source;
    public final String site;
    public final Cookie cookie;

    public CookieBundle(int version, String source, String site, Cookie cookie) {
        this.version = version;
        this.source = Objects.requireNonNull(source);
        this.site = Objects.requireNonNull(site);
        this.cookie = Objects.requireNonNull(cookie);
    }

    public CookieBundle(String source, String site, Cookie cookie) {
        this(VERSION, source, site, cookie);
    }

    /** Reads the wire form. A missing or null member is the zero value, as in Go's decoder. */
    public static CookieBundle fromJson(String json) {
        return fromObject(Json.object(Json.parse(json), "a cookie bundle"));
    }

    static CookieBundle fromObject(Map<String, Object> o) {
        Object cookie = o.get("cookie");
        return new CookieBundle((int) Json.integer(o, "version"), Json.string(o, "source"), Json.string(o, "site"),
                cookie == null ? Cookie.builder("", "", "", "").build() : Cookie.fromObject(Json.object(cookie, "cookie")));
    }

    public String toJson() {
        StringBuilder out = new StringBuilder("{\"version\":").append(version).append(",\"source\":");
        GoText.jsonQuote(out, source).append(",\"site\":");
        GoText.jsonQuote(out, site).append(",\"cookie\":").append(cookie.toJson());
        return out.append('}').toString();
    }

    @Override
    public boolean equals(Object other) {
        if (!(other instanceof CookieBundle)) {
            return false;
        }
        CookieBundle b = (CookieBundle) other;
        return version == b.version && source.equals(b.source) && site.equals(b.site) && cookie.equals(b.cookie);
    }

    @Override
    public int hashCode() {
        return Objects.hash(version, source, site, cookie);
    }

    /** The bundle with the cookie's value redacted. */
    @Override
    public String toString() {
        StringBuilder out = new StringBuilder("cookie bundle{version=").append(version).append(" source=");
        GoText.jsonQuote(out, source).append(" site=");
        GoText.jsonQuote(out, site).append(' ').append(cookie);
        return out.append('}').toString();
    }
}
