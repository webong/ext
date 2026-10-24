package io.github.webong.ext.web;

import java.util.Arrays;
import java.util.Map;

/** The portable cookie rules of {@code res/web/contract}. */
public final class CookieRules {
    private CookieRules() {}

    private static long nowSeconds() {
        return System.currentTimeMillis() / 1000;
    }

    /** {@code contract.CookieActive}: a session cookie, or one that expires after now. */
    public static boolean isActive(Cookie cookie) {
        return isActive(cookie, nowSeconds());
    }

    public static boolean isActive(Cookie cookie, long nowEpochSeconds) {
        return cookie.expiry == 0 || cookie.expiry > nowEpochSeconds;
    }

    /** {@code contract.CookieDomainMatches}. */
    public static boolean domainMatches(String siteHost, String cookieDomain) {
        String host = GoText.lowercased(siteHost);
        if (host.endsWith(".")) {
            host = host.substring(0, host.length() - 1);
        }
        String domain = GoText.lowercased(cookieDomain);
        if (domain.startsWith(".")) {
            String base = domain.substring(1);
            return host.equals(base) || host.endsWith("." + base);
        }
        return host.equals(domain);
    }

    /** {@code contract.CookiePathMatches}: a prefix matches only at a path boundary. */
    public static boolean pathMatches(String requestPath, String cookiePath) {
        byte[] request = GoText.utf8(requestPath.isEmpty() ? "/" : requestPath);
        byte[] cookie = GoText.utf8(cookiePath);
        if (cookie.length == 0 || request.length < cookie.length
                || !Arrays.equals(Arrays.copyOfRange(request, 0, cookie.length), cookie)) {
            return false;
        }
        return request.length == cookie.length || cookie[cookie.length - 1] == '/' || request[cookie.length] == '/';
    }

    /** {@code contract.CookieMatchesSiteOptions}; a null site matches on expiry alone. */
    public static boolean matches(Site site, Cookie cookie, boolean includeExpired) {
        return matches(site, cookie, includeExpired, nowSeconds());
    }

    public static boolean matches(Site site, Cookie cookie, boolean includeExpired, long nowEpochSeconds) {
        if (site == null) {
            return includeExpired || isActive(cookie, nowEpochSeconds);
        }
        return domainMatches(site.hostname, cookie.domain)
                && (site.path.isEmpty() || pathMatches(site.escapedPath, cookie.path))
                && (!cookie.secure || site.scheme.equals("https"))
                && (includeExpired || isActive(cookie, nowEpochSeconds));
    }

    /** {@code contract.SameListedCookie}: the fields that identify a cookie in a listing. */
    public static boolean sameListed(Cookie a, Cookie b) {
        return a.id == b.id && a.ref.equals(b.ref) && a.name.equals(b.name) && a.domain.equals(b.domain)
                && a.path.equals(b.path) && a.partitionKey.equals(b.partitionKey)
                && a.hasCrossSiteAncestor == b.hasCrossSiteAncestor && a.attributes.equals(b.attributes);
    }

    /**
     * {@code contract.ValidateCookie}: the portable field rules, without requiring an unexpired
     * cookie or a particular site.
     */
    public static void validate(Cookie cookie) throws CookieException {
        if (cookie.name.isEmpty() || GoText.containsAny(cookie.name, "()<>@,;:\\\"/[]?={} \t") || GoText.hasControl(cookie.name)) {
            throw new CookieException("cookie has an invalid name");
        }
        String host = cookie.domain.startsWith(".") ? cookie.domain.substring(1) : cookie.domain;
        if (host.isEmpty() || GoText.containsAny(host, " /\\?#@\t\r\n") || GoText.hasControl(host)
                || (host.indexOf(':') >= 0 && !IPLiteral.isIP(host)) || host.startsWith(".")) {
            throw new CookieException("cookie has an invalid domain");
        }
        if (!cookie.path.startsWith("/") || GoText.hasControl(cookie.path) || GoText.hasControl(cookie.value)) {
            throw new CookieException("cookie has an invalid path or value");
        }
        if ((cookie.name.startsWith("__Secure-") && !cookie.secure)
                || (cookie.name.startsWith("__Host-")
                        && (!cookie.secure || cookie.domain.startsWith(".") || !cookie.path.equals("/")))
                || (cookie.sameSitePolicy.equals("none") && !cookie.secure)) {
            throw new CookieException("cookie violates secure prefix or SameSite requirements");
        }
        // This upper bound also keeps native microsecond expiry conversions safe.
        if (cookie.expiry > 253402300799L) {
            throw new CookieException("cookie expiry exceeds the portable range");
        }
        if (GoText.hasControl(cookie.partitionKey) || (cookie.hasCrossSiteAncestor && cookie.partitionKey.isEmpty())) {
            throw new CookieException("cookie has an invalid partition scope");
        }
        if (cookie.attributes.size() > 32) {
            throw new CookieException("cookie has too many adapter attributes");
        }
        for (Map.Entry<String, String> entry : cookie.attributes.entrySet()) {
            String key = entry.getKey();
            if (key.indexOf('.') < 0 || GoText.utf8Length(key) > 128 || GoText.utf8Length(entry.getValue()) > 1024
                    || GoText.containsAny(key, " \t\r\n")) {
                throw new CookieException("cookie has an invalid adapter attribute");
            }
        }
    }

    /**
     * {@code contract.ValidateCookieBundle}: the site parses, the cookie validates, is active and
     * matches the site, and SameSite is one the contract knows.
     */
    public static void validate(CookieBundle bundle) throws CookieException {
        validate(bundle, nowSeconds());
    }

    public static void validate(CookieBundle bundle, long nowEpochSeconds) throws CookieException {
        if (bundle.version != CookieBundle.VERSION) {
            throw new CookieException("unsupported browser cookie bundle version");
        }
        Site site;
        try {
            site = Site.parse(bundle.site);
        } catch (SiteException e) {
            throw new CookieException(e.getMessage());
        }
        validate(bundle.cookie);
        if (!matches(site, bundle.cookie, false, nowEpochSeconds)) {
            throw new CookieException("cookie bundle has an invalid or expired site scope");
        }
        switch (bundle.cookie.sameSitePolicy) {
            case "":
            case "unspecified":
            case "none":
            case "lax":
            case "strict":
                break;
            default:
                throw new CookieException("cookie bundle has an unsupported SameSite policy");
        }
    }
}
