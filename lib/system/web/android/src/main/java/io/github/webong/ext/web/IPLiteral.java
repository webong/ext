package io.github.webong.ext.web;

/**
 * IP address literals as Go's net/netip.ParseAddr reads them: what net/url accepts between
 * brackets and what net.ParseIP accepts for a cookie domain.
 */
final class IPLiteral {
    private IPLiteral() {}

    static final int NOT_AN_ADDRESS = 0;
    static final int V4 = 1;
    static final int V6 = 2;
    static final int V6_WITH_ZONE = 3;

    /** netip.ParseAddr: {@link #NOT_AN_ADDRESS}, {@link #V4}, {@link #V6} or {@link #V6_WITH_ZONE}. */
    static int parseAddr(byte[] text) {
        for (byte b : text) {
            switch (b) {
                case '.':
                    return parseIPv4(text, 0, text.length) ? V4 : NOT_AN_ADDRESS;
                case ':':
                    return parseIPv6(text);
                case '%':
                    return NOT_AN_ADDRESS;
                default:
                    break;
            }
        }
        return NOT_AN_ADDRESS;
    }

    /** net.ParseIP(text) != nil: an address without a zone. */
    static boolean isIP(String text) {
        int parsed = parseAddr(GoText.utf8(text));
        return parsed == V4 || parsed == V6;
    }

    private static boolean parseIPv4(byte[] text, int start, int end) {
        int value = 0;
        int position = 0;
        int digits = 0;
        for (int i = start; i < end; i++) {
            byte b = text[i];
            if (b >= '0' && b <= '9') {
                if (digits == 1 && value == 0) {
                    return false;
                }
                value = value * 10 + (b - '0');
                digits++;
                if (value > 255) {
                    return false;
                }
            } else if (b == '.') {
                if (i == start || i == end - 1 || text[i - 1] == '.') {
                    return false;
                }
                if (position == 3) {
                    return false;
                }
                position++;
                value = 0;
                digits = 0;
            } else {
                return false;
            }
        }
        return position >= 3;
    }

    private static int parseIPv6(byte[] input) {
        int end = input.length;
        boolean hasZone = false;
        for (int i = 0; i < input.length; i++) {
            if (input[i] == '%') {
                if (i == input.length - 1) {
                    return NOT_AN_ADDRESS;
                }
                end = i;
                hasZone = true;
                break;
            }
        }
        int zoneResult = hasZone ? V6_WITH_ZONE : V6;
        int at = 0;
        int filled = 0;
        int ellipsis = -1;
        if (end - at >= 2 && input[at] == ':' && input[at + 1] == ':') {
            ellipsis = 0;
            at += 2;
            if (at == end) {
                return zoneResult;
            }
        }
        while (filled < 16) {
            int offset = 0;
            while (at + offset < end) {
                byte b = input[at + offset];
                boolean hex = (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F');
                if (!hex) {
                    break;
                }
                if (offset > 3) {
                    return NOT_AN_ADDRESS;
                }
                offset++;
            }
            if (offset == 0) {
                return NOT_AN_ADDRESS;
            }
            if (at + offset < end && input[at + offset] == '.') {
                if (ellipsis < 0 && filled != 12) {
                    return NOT_AN_ADDRESS;
                }
                if (filled + 4 > 16) {
                    return NOT_AN_ADDRESS;
                }
                if (!parseIPv4(input, at, end)) {
                    return NOT_AN_ADDRESS;
                }
                at = end;
                filled += 4;
                break;
            }
            filled += 2;
            at += offset;
            if (at == end) {
                break;
            }
            if (input[at] != ':') {
                return NOT_AN_ADDRESS;
            }
            if (end - at == 1) {
                return NOT_AN_ADDRESS;
            }
            at++;
            if (input[at] == ':') {
                if (ellipsis >= 0) {
                    return NOT_AN_ADDRESS;
                }
                ellipsis = filled;
                at++;
                if (at == end) {
                    break;
                }
            }
        }
        if (at != end) {
            return NOT_AN_ADDRESS;
        }
        if (filled < 16) {
            if (ellipsis < 0) {
                return NOT_AN_ADDRESS;
            }
        } else if (ellipsis >= 0) {
            return NOT_AN_ADDRESS;
        }
        return zoneResult;
    }
}
