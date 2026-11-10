package io.github.webong.ext.network;

import java.util.Locale;
import java.util.Objects;

public final class NetworkAddress {
    private final String ip;
    private final int prefixLength;
    private final String family;
    private final String scope;

    public NetworkAddress(String ip, int prefixLength, String family, String scope) {
        if (ip == null || ip.isEmpty() || prefixLength < 0 || family == null || scope == null) {
            throw new IllegalArgumentException("network address fields are invalid");
        }
        this.ip = ip; this.prefixLength = prefixLength; this.family = family; this.scope = scope;
    }

    /**
     * Builds an address from a literal IPv4 or IPv6 address and a prefix length: the canonical text
     * (RFC 5952, lower case), the family and the scope (testdata/network/v1). It refuses a zone, a
     * bracket, a leading zero in an IPv4 octet, a host name and a prefix beyond the family's width.
     *
     * @throws IllegalArgumentException when the address or the prefix is not valid
     */
    public static NetworkAddress of(String ip, int prefixLength) {
        int[] bytes = parse(ip);
        boolean v4 = bytes.length == 4;
        if (prefixLength < 0 || prefixLength > (v4 ? 32 : 128)) {
            throw new IllegalArgumentException("network prefix length is out of range");
        }
        return new NetworkAddress(format(bytes), prefixLength, v4 ? "ipv4" : "ipv6", scope(bytes));
    }

    /** The canonical text of a literal address (see {@link #of}). */
    public static String canonical(String ip) {
        return format(parse(ip));
    }

    private static int[] parse(String text) {
        if (text == null || text.isEmpty()) throw new IllegalArgumentException("network address is empty");
        return text.indexOf(':') >= 0 ? parseV6(text) : parseV4(text);
    }

    private static int[] parseV4(String text) {
        String[] parts = text.split("\\.", -1);
        if (parts.length != 4) throw new IllegalArgumentException("network address is not an IPv4 address");
        int[] out = new int[4];
        for (int i = 0; i < 4; i++) {
            String part = parts[i];
            if (part.isEmpty() || part.length() > 3 || (part.length() > 1 && part.charAt(0) == '0')) {
                throw new IllegalArgumentException("network address is not an IPv4 address");
            }
            int value = 0;
            for (int j = 0; j < part.length(); j++) {
                char c = part.charAt(j);
                if (c < '0' || c > '9') throw new IllegalArgumentException("network address is not an IPv4 address");
                value = value * 10 + (c - '0');
            }
            if (value > 255) throw new IllegalArgumentException("network address is not an IPv4 address");
            out[i] = value;
        }
        return out;
    }

    private static int[] parseV6(String text) {
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (!(c == ':' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')) {
                throw new IllegalArgumentException("network address is not an IPv6 address");
            }
        }
        if (text.contains(":::")) throw new IllegalArgumentException("network address is not an IPv6 address");
        int gap = text.indexOf("::");
        if (gap >= 0 && text.indexOf("::", gap + 1) >= 0) throw new IllegalArgumentException("network address is not an IPv6 address");
        int[] head = gap < 0 ? groups(text, true) : groups(text.substring(0, gap), false);
        int[] tail = gap < 0 ? new int[0] : groups(text.substring(gap + 2), true);
        if (gap < 0 ? head.length != 8 : head.length + tail.length > 7) {
            throw new IllegalArgumentException("network address is not an IPv6 address");
        }
        int[] groups = new int[8];
        System.arraycopy(head, 0, groups, 0, head.length);
        System.arraycopy(tail, 0, groups, 8 - tail.length, tail.length);
        int[] out = new int[16];
        for (int i = 0; i < 8; i++) {
            out[2 * i] = groups[i] >> 8;
            out[2 * i + 1] = groups[i] & 0xff;
        }
        return out;
    }

    /** The 16-bit groups of one side of a "::"; the last token of the address may be an IPv4 address. */
    private static int[] groups(String text, boolean mayEndWithV4) {
        if (text.isEmpty()) return new int[0];
        String[] tokens = text.split(":", -1);
        int[] out = new int[tokens.length + 1];
        int n = 0;
        for (int i = 0; i < tokens.length; i++) {
            String token = tokens[i];
            if (token.isEmpty()) throw new IllegalArgumentException("network address is not an IPv6 address");
            if (token.indexOf('.') >= 0) {
                if (!mayEndWithV4 || i != tokens.length - 1) throw new IllegalArgumentException("network address is not an IPv6 address");
                int[] v4 = parseV4(token);
                out[n++] = v4[0] << 8 | v4[1];
                out[n++] = v4[2] << 8 | v4[3];
            } else {
                if (token.length() > 4) throw new IllegalArgumentException("network address is not an IPv6 address");
                out[n++] = Integer.parseInt(token, 16);
            }
        }
        return java.util.Arrays.copyOf(out, n);
    }

    private static String format(int[] bytes) {
        StringBuilder out = new StringBuilder();
        if (bytes.length == 4) {
            return out.append(bytes[0]).append('.').append(bytes[1]).append('.').append(bytes[2]).append('.').append(bytes[3]).toString();
        }
        int[] groups = new int[8];
        for (int i = 0; i < 8; i++) groups[i] = bytes[2 * i] << 8 | bytes[2 * i + 1];
        int bestStart = -1;
        int bestLength = 0;
        for (int i = 0; i < 8; ) {
            if (groups[i] != 0) { i++; continue; }
            int j = i;
            while (j < 8 && groups[j] == 0) j++;
            if (j - i > bestLength) { bestStart = i; bestLength = j - i; }
            i = j;
        }
        if (bestLength < 2) bestStart = -1;
        for (int i = 0; i < 8; i++) {
            if (i == bestStart) {
                out.append("::");
                i += bestLength - 1;
                continue;
            }
            if (out.length() > 0 && out.charAt(out.length() - 1) != ':') out.append(':');
            out.append(Integer.toHexString(groups[i]));
        }
        return out.toString().toLowerCase(Locale.ROOT);
    }

    /** The scope of an address: IANA's special-purpose ranges, as testdata/network/v1 lists them. */
    private static String scope(int[] b) {
        if (b.length == 4) {
            if (b[0] == 127) return "loopback";
            if (b[0] == 169 && b[1] == 254) return "link";
            if (b[0] == 224 && b[1] == 0 && b[2] == 0) return "link";
            if (b[0] == 10 || b[0] == 172 && b[1] >= 16 && b[1] <= 31 || b[0] == 192 && b[1] == 168) return "private";
            if (b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0) return "unspecified";
            if (b[0] >= 224 && b[0] <= 239) return "multicast";
            return "global";
        }
        boolean zeros = true;
        for (int i = 0; i < 15; i++) zeros &= b[i] == 0;
        if (zeros && b[15] == 1) return "loopback";
        if (b[0] == 0xfe && (b[1] & 0xc0) == 0x80) return "link";
        if (b[0] == 0xff && (b[1] & 0x0f) == 0x02) return "link";
        if ((b[0] & 0xfe) == 0xfc) return "private";
        if (zeros && b[15] == 0) return "unspecified";
        if (b[0] == 0xff) return "multicast";
        return "global";
    }

    public String ip() { return ip; }
    public int prefixLength() { return prefixLength; }
    public String family() { return family; }
    public String scope() { return scope; }
    @Override public boolean equals(Object other) {
        if (!(other instanceof NetworkAddress)) return false;
        NetworkAddress value = (NetworkAddress) other;
        return ip.equals(value.ip) && prefixLength == value.prefixLength && family.equals(value.family) && scope.equals(value.scope);
    }
    @Override public int hashCode() { return Objects.hash(ip, prefixLength, family, scope); }
}
