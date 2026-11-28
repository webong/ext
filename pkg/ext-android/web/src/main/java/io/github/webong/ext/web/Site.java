package io.github.webong.ext.web;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;

/**
 * A parsed http or https site, as {@code contract.ParseSite} reads it: the URL Go's net/url would
 * build, with the host lower-cased. The rules are Go's net/url (Go 1.27), written out for this
 * input. A host with two or more colons outside brackets is refused, as Go does with
 * {@code urlstrictcolons} on; Go's default for that depends on the consuming module's {@code go}
 * line.
 */
public final class Site {
    /** {@code http} or {@code https}. */
    public final String scheme;
    /** The host with its port, decoded and lower-cased. */
    public final String host;
    /** The host without its port or brackets. */
    public final String hostname;
    /** The decoded path; empty when the URL has none. */
    public final String path;
    /** The path as it would be sent (Go's EscapedPath). */
    public final String escapedPath;

    public final String rawQuery;
    /** A trailing {@code ?} with nothing after it. */
    public final boolean forceQuery;

    public final String fragment;
    public final String escapedFragment;

    private Site(String scheme, String host, String hostname, String path, String escapedPath, String rawQuery,
            boolean forceQuery, String fragment, String escapedFragment) {
        this.scheme = scheme;
        this.host = host;
        this.hostname = hostname;
        this.path = path;
        this.escapedPath = escapedPath;
        this.rawQuery = rawQuery;
        this.forceQuery = forceQuery;
        this.fragment = fragment;
        this.escapedFragment = escapedFragment;
    }

    /** The canonical form (Go's URL.String). */
    public String string() {
        StringBuilder out = new StringBuilder(scheme).append(':');
        if (!host.isEmpty() || !path.isEmpty()) {
            out.append("//");
        }
        out.append(new String(UrlEscape.escape(GoText.utf8(host), UrlEscape.Mode.HOST), StandardCharsets.ISO_8859_1));
        if (!escapedPath.isEmpty() && !escapedPath.startsWith("/") && !host.isEmpty()) {
            out.append('/');
        }
        out.append(escapedPath);
        if (forceQuery || !rawQuery.isEmpty()) {
            out.append('?').append(rawQuery);
        }
        if (!fragment.isEmpty()) {
            out.append('#').append(escapedFragment);
        }
        return out.toString();
    }

    @Override
    public String toString() {
        return string();
    }

    /**
     * Parses a site the way {@code contract.ParseSite} does: http or https only, a hostname, no
     * userinfo, and a lower-cased host.
     */
    public static Site parse(String raw) throws SiteException {
        try {
            return parseBytes(GoText.utf8(raw));
        } catch (UrlEscape.Invalid e) {
            throw new SiteException();
        }
    }

    private static Site parseBytes(byte[] input) throws SiteException, UrlEscape.Invalid {
        byte[] bytes = input;
        byte[] fragmentBytes = new byte[0];
        int hash = indexOf(bytes, (byte) '#', 0);
        if (hash >= 0) {
            fragmentBytes = Arrays.copyOfRange(bytes, hash + 1, bytes.length);
            bytes = Arrays.copyOfRange(bytes, 0, hash);
        }
        for (byte b : bytes) {
            if ((b >= 0 && b < 0x20) || b == 0x7F) {
                throw new SiteException();
            }
        }

        // getScheme: letters, then letters, digits and "+-." up to the colon.
        int colon = -1;
        for (int i = 0; i < bytes.length; i++) {
            byte b = bytes[i];
            if ((b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')) {
                continue;
            }
            if (b == ':') {
                if (i == 0) {
                    throw new SiteException();
                }
                colon = i;
                break;
            }
            if (i > 0 && ((b >= '0' && b <= '9') || b == '+' || b == '-' || b == '.')) {
                continue;
            }
            break;
        }
        if (colon < 0) {
            throw new SiteException();
        }
        String scheme = new String(Arrays.copyOfRange(bytes, 0, colon), StandardCharsets.US_ASCII).toLowerCase(java.util.Locale.ROOT);
        if (!scheme.equals("http") && !scheme.equals("https")) {
            throw new SiteException();
        }
        byte[] rest = Arrays.copyOfRange(bytes, colon + 1, bytes.length);

        boolean forceQuery = false;
        byte[] rawQuery = new byte[0];
        if (rest.length > 0 && rest[rest.length - 1] == '?' && count(rest, (byte) '?') == 1) {
            forceQuery = true;
            rest = Arrays.copyOfRange(rest, 0, rest.length - 1);
        } else {
            int question = indexOf(rest, (byte) '?', 0);
            if (question >= 0) {
                rawQuery = Arrays.copyOfRange(rest, question + 1, rest.length);
                rest = Arrays.copyOfRange(rest, 0, question);
            }
        }

        // A rootless path is opaque and a path without "//" has no host: both are refused.
        if (rest.length < 2 || rest[0] != '/' || rest[1] != '/') {
            throw new SiteException();
        }
        byte[] authority = Arrays.copyOfRange(rest, 2, rest.length);
        byte[] rawPath = new byte[0];
        int slash = indexOf(authority, (byte) '/', 0);
        if (slash >= 0) {
            rawPath = Arrays.copyOfRange(authority, slash, authority.length);
            authority = Arrays.copyOfRange(authority, 0, slash);
        }
        if (indexOf(authority, (byte) '@', 0) >= 0) {
            throw new SiteException();
        }
        byte[] decodedHost = parseHost(authority);
        if (splitHostPort(decodedHost).length == 0) {
            throw new SiteException();
        }

        // strings.ToLower on the decoded host, which turns every invalid byte into U+FFFD.
        byte[] host;
        boolean ascii = true;
        for (byte b : decodedHost) {
            if (b < 0) {
                ascii = false;
                break;
            }
        }
        if (ascii) {
            host = decodedHost.clone();
            for (int i = 0; i < host.length; i++) {
                if (host[i] >= 'A' && host[i] <= 'Z') {
                    host[i] += 0x20;
                }
            }
        } else {
            host = GoText.utf8(GoText.lowercased(GoText.decodeLossy(decodedHost)));
        }

        byte[] path = UrlEscape.unescape(rawPath, UrlEscape.Mode.PATH);
        byte[] escapedPath;
        if (rawPath.length > 0 && !Arrays.equals(rawPath, UrlEscape.escape(path, UrlEscape.Mode.PATH))
                && UrlEscape.validEncoded(rawPath, UrlEscape.Mode.PATH)
                && Arrays.equals(UrlEscape.unescape(rawPath, UrlEscape.Mode.PATH), path)) {
            escapedPath = rawPath;
        } else if (path.length == 1 && path[0] == '*') {
            escapedPath = path;
        } else {
            escapedPath = UrlEscape.escape(path, UrlEscape.Mode.PATH);
        }

        byte[] decodedFragment = new byte[0];
        byte[] escapedFragment = new byte[0];
        if (fragmentBytes.length > 0) {
            decodedFragment = UrlEscape.unescape(fragmentBytes, UrlEscape.Mode.FRAGMENT);
            if (!Arrays.equals(fragmentBytes, UrlEscape.escape(decodedFragment, UrlEscape.Mode.FRAGMENT))
                    && UrlEscape.validEncoded(fragmentBytes, UrlEscape.Mode.FRAGMENT)
                    && Arrays.equals(UrlEscape.unescape(fragmentBytes, UrlEscape.Mode.FRAGMENT), decodedFragment)) {
                escapedFragment = fragmentBytes;
            } else {
                escapedFragment = UrlEscape.escape(decodedFragment, UrlEscape.Mode.FRAGMENT);
            }
        }

        // Text the way Go's range loop sees bytes: each invalid byte is one U+FFFD.
        return new Site(scheme, GoText.decodeLossy(host), GoText.decodeLossy(splitHostPort(host)), GoText.decodeLossy(path),
                text(escapedPath), text(rawQuery), forceQuery, GoText.decodeLossy(decodedFragment), text(escapedFragment));
    }

    /** Text from bytes that are ASCII (escaped output) or that Go keeps as given (the raw query). */
    private static String text(byte[] bytes) {
        return new String(bytes, StandardCharsets.UTF_8);
    }

    /** net/url parseHost for an http or https authority. */
    private static byte[] parseHost(byte[] host) throws SiteException, UrlEscape.Invalid {
        int open = lastIndexOf(host, (byte) '[');
        if (open > 0) {
            throw new SiteException();
        }
        if (host.length > 0 && host[0] == '[') {
            int close = lastIndexOf(host, (byte) ']');
            if (close < 0) {
                throw new SiteException();
            }
            byte[] colonPort = Arrays.copyOfRange(host, close + 1, host.length);
            if (!validOptionalPort(colonPort)) {
                throw new SiteException();
            }
            byte[] hostname = Arrays.copyOfRange(host, 1, close);
            byte[] unescaped;
            int zone = indexOf(hostname, new byte[] {'%', '2', '5'});
            if (zone >= 0) {
                byte[] hostPart = UrlEscape.unescape(Arrays.copyOfRange(hostname, 0, zone), UrlEscape.Mode.HOST);
                byte[] zonePart = UrlEscape.unescape(Arrays.copyOfRange(hostname, zone, hostname.length), UrlEscape.Mode.ZONE);
                unescaped = concat(hostPart, zonePart);
            } else {
                unescaped = UrlEscape.unescape(hostname, UrlEscape.Mode.HOST);
            }
            // Only an IPv6 address may be bracketed.
            int family = IPLiteral.parseAddr(unescaped);
            if (family != IPLiteral.V6 && family != IPLiteral.V6_WITH_ZONE) {
                throw new SiteException();
            }
            return concat(new byte[] {'['}, unescaped, new byte[] {']'}, colonPort);
        }
        int first = indexOf(host, (byte) ':', 0);
        if (first >= 0) {
            // The port after the first colon must be digits, so a second colon is refused too.
            if (!validOptionalPort(Arrays.copyOfRange(host, first, host.length))) {
                throw new SiteException();
            }
        }
        return UrlEscape.unescape(host, UrlEscape.Mode.HOST);
    }

    private static boolean validOptionalPort(byte[] port) {
        if (port.length == 0) {
            return true;
        }
        if (port[0] != ':') {
            return false;
        }
        for (int i = 1; i < port.length; i++) {
            if (port[i] < '0' || port[i] > '9') {
                return false;
            }
        }
        return true;
    }

    /** net/url splitHostPort, returning the host. */
    private static byte[] splitHostPort(byte[] hostPort) {
        byte[] host = hostPort;
        int colon = lastIndexOf(host, (byte) ':');
        if (colon >= 0 && validOptionalPort(Arrays.copyOfRange(host, colon, host.length))) {
            host = Arrays.copyOfRange(host, 0, colon);
        }
        if (host.length >= 2 && host[0] == '[' && host[host.length - 1] == ']') {
            host = Arrays.copyOfRange(host, 1, host.length - 1);
        }
        return host;
    }

    private static int indexOf(byte[] bytes, byte value, int from) {
        for (int i = from; i < bytes.length; i++) {
            if (bytes[i] == value) {
                return i;
            }
        }
        return -1;
    }

    private static int lastIndexOf(byte[] bytes, byte value) {
        for (int i = bytes.length - 1; i >= 0; i--) {
            if (bytes[i] == value) {
                return i;
            }
        }
        return -1;
    }

    private static int indexOf(byte[] haystack, byte[] needle) {
        for (int i = 0; i + needle.length <= haystack.length; i++) {
            if (Arrays.equals(Arrays.copyOfRange(haystack, i, i + needle.length), needle)) {
                return i;
            }
        }
        return -1;
    }

    private static int count(byte[] bytes, byte value) {
        int n = 0;
        for (byte b : bytes) {
            if (b == value) {
                n++;
            }
        }
        return n;
    }

    private static byte[] concat(byte[]... parts) {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        for (byte[] part : parts) {
            out.write(part, 0, part.length);
        }
        return out.toByteArray();
    }
}
