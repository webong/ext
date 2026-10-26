package io.github.webong.ext.network;

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
