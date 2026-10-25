package io.github.webong.ext.web;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Objects;
import java.util.TreeMap;

/**
 * One browser cookie in the portable wire form ({@code res/web/contract}, wire version 2).
 *
 * <p>A cookie value is a credential. {@link #toString()} prints the cookie's identity (name,
 * domain, path) and never its value; {@link #toJson()} keeps the real value because the protocol
 * needs it, so treat that output as secret.
 */
public final class Cookie {
    public final long id;
    public final String ref;
    public final String name;
    public final String value;
    public final String domain;
    public final String path;
    /** Unix seconds. 0 is a session cookie, 1 is already expired. */
    public final long expiry;

    public final boolean secure;
    public final boolean httpOnly;
    /** {@code ""}, {@code unspecified}, {@code none}, {@code lax} or {@code strict}. */
    public final String sameSitePolicy;

    public final String partitionKey;
    public final boolean hasCrossSiteAncestor;
    /** Adapter-owned fields with namespaced keys (they contain a dot). Never null. */
    public final Map<String, String> attributes;

    private Cookie(Builder b) {
        this.id = b.id;
        this.ref = b.ref;
        this.name = b.name;
        this.value = b.value;
        this.domain = b.domain;
        this.path = b.path;
        this.expiry = b.expiry;
        this.secure = b.secure;
        this.httpOnly = b.httpOnly;
        this.sameSitePolicy = b.sameSitePolicy;
        this.partitionKey = b.partitionKey;
        this.hasCrossSiteAncestor = b.hasCrossSiteAncestor;
        this.attributes = Collections.unmodifiableMap(new LinkedHashMap<>(b.attributes));
    }

    public static Builder builder(String name, String value, String domain, String path) {
        return new Builder(name, value, domain, path);
    }

    /** A builder that starts from this cookie. */
    public Builder toBuilder() {
        Builder b = new Builder(name, value, domain, path);
        b.id = id;
        b.ref = ref;
        b.expiry = expiry;
        b.secure = secure;
        b.httpOnly = httpOnly;
        b.sameSitePolicy = sameSitePolicy;
        b.partitionKey = partitionKey;
        b.hasCrossSiteAncestor = hasCrossSiteAncestor;
        b.attributes.putAll(attributes);
        return b;
    }

    public static final class Builder {
        private long id;
        private String ref = "";
        private final String name;
        private final String value;
        private final String domain;
        private final String path;
        private long expiry;
        private boolean secure;
        private boolean httpOnly;
        private String sameSitePolicy = "";
        private String partitionKey = "";
        private boolean hasCrossSiteAncestor;
        private final Map<String, String> attributes = new LinkedHashMap<>();

        private Builder(String name, String value, String domain, String path) {
            this.name = Objects.requireNonNull(name);
            this.value = Objects.requireNonNull(value);
            this.domain = Objects.requireNonNull(domain);
            this.path = Objects.requireNonNull(path);
        }

        public Builder id(long id) {
            this.id = id;
            return this;
        }

        public Builder ref(String ref) {
            this.ref = Objects.requireNonNull(ref);
            return this;
        }

        public Builder expiry(long expiry) {
            this.expiry = expiry;
            return this;
        }

        public Builder secure(boolean secure) {
            this.secure = secure;
            return this;
        }

        public Builder httpOnly(boolean httpOnly) {
            this.httpOnly = httpOnly;
            return this;
        }

        public Builder sameSitePolicy(String sameSitePolicy) {
            this.sameSitePolicy = Objects.requireNonNull(sameSitePolicy);
            return this;
        }

        public Builder partitionKey(String partitionKey) {
            this.partitionKey = Objects.requireNonNull(partitionKey);
            return this;
        }

        public Builder hasCrossSiteAncestor(boolean hasCrossSiteAncestor) {
            this.hasCrossSiteAncestor = hasCrossSiteAncestor;
            return this;
        }

        public Builder attribute(String key, String attributeValue) {
            this.attributes.put(Objects.requireNonNull(key), Objects.requireNonNull(attributeValue));
            return this;
        }

        public Cookie build() {
            return new Cookie(this);
        }
    }

    /** Reads the wire form. A missing or null member is the zero value, as in Go's decoder. */
    public static Cookie fromJson(String json) {
        return fromObject(Json.object(Json.parse(json), "a cookie"));
    }

    static Cookie fromObject(Map<String, Object> o) {
        Builder b = new Builder(Json.string(o, "name"), Json.string(o, "value"), Json.string(o, "domain"), Json.string(o, "path"))
                .id(Json.integer(o, "id"))
                .ref(Json.string(o, "ref"))
                .expiry(Json.integer(o, "expiry"))
                .secure(Json.bool(o, "secure"))
                .httpOnly(Json.bool(o, "http_only"))
                .sameSitePolicy(Json.string(o, "same_site_policy"))
                .partitionKey(Json.string(o, "partition_key"))
                .hasCrossSiteAncestor(Json.bool(o, "has_cross_site_ancestor"));
        Object attributes = o.get("attributes");
        if (attributes != null) {
            for (Map.Entry<String, Object> entry : Json.object(attributes, "attributes").entrySet()) {
                if (!(entry.getValue() instanceof String)) {
                    throw new IllegalArgumentException("attributes must hold strings");
                }
                b.attribute(entry.getKey(), (String) entry.getValue());
            }
        }
        return b.build();
    }

    /** The wire form. {@code id}, {@code ref}, {@code same_site_policy}, {@code partition_key}, {@code has_cross_site_ancestor} and {@code attributes} are written only when set. */
    public String toJson() {
        StringBuilder out = new StringBuilder("{");
        if (id != 0) {
            out.append("\"id\":").append(id).append(',');
        }
        if (!ref.isEmpty()) {
            GoText.jsonQuote(out.append("\"ref\":"), ref).append(',');
        }
        GoText.jsonQuote(out.append("\"name\":"), name).append(',');
        GoText.jsonQuote(out.append("\"value\":"), value).append(',');
        GoText.jsonQuote(out.append("\"domain\":"), domain).append(',');
        GoText.jsonQuote(out.append("\"path\":"), path).append(',');
        out.append("\"expiry\":").append(expiry).append(',');
        out.append("\"secure\":").append(secure).append(',');
        out.append("\"http_only\":").append(httpOnly);
        if (!sameSitePolicy.isEmpty()) {
            GoText.jsonQuote(out.append(",\"same_site_policy\":"), sameSitePolicy);
        }
        if (!partitionKey.isEmpty()) {
            GoText.jsonQuote(out.append(",\"partition_key\":"), partitionKey);
        }
        if (hasCrossSiteAncestor) {
            out.append(",\"has_cross_site_ancestor\":true");
        }
        if (!attributes.isEmpty()) {
            out.append(",\"attributes\":{");
            boolean first = true;
            for (Map.Entry<String, String> entry : new TreeMap<>(attributes).entrySet()) {
                if (!first) {
                    out.append(',');
                }
                first = false;
                GoText.jsonQuote(out, entry.getKey()).append(':');
                GoText.jsonQuote(out, entry.getValue());
            }
            out.append('}');
        }
        return out.append('}').toString();
    }

    @Override
    public boolean equals(Object other) {
        if (!(other instanceof Cookie)) {
            return false;
        }
        Cookie c = (Cookie) other;
        return id == c.id && ref.equals(c.ref) && name.equals(c.name) && value.equals(c.value) && domain.equals(c.domain)
                && path.equals(c.path) && expiry == c.expiry && secure == c.secure && httpOnly == c.httpOnly
                && sameSitePolicy.equals(c.sameSitePolicy) && partitionKey.equals(c.partitionKey)
                && hasCrossSiteAncestor == c.hasCrossSiteAncestor && attributes.equals(c.attributes);
    }

    @Override
    public int hashCode() {
        return Objects.hash(id, ref, name, value, domain, path, expiry, secure, httpOnly, sameSitePolicy, partitionKey,
                hasCrossSiteAncestor, attributes);
    }

    /** The cookie without its value. The wording is not part of the contract. */
    @Override
    public String toString() {
        StringBuilder out = new StringBuilder("cookie{name=");
        GoText.jsonQuote(out, name).append(" domain=");
        GoText.jsonQuote(out, domain).append(" path=");
        GoText.jsonQuote(out, path);
        if (!value.isEmpty()) {
            out.append(" value=<redacted>");
        }
        if (!partitionKey.isEmpty()) {
            GoText.jsonQuote(out.append(" partition="), partitionKey);
        }
        return out.append('}').toString();
    }
}
