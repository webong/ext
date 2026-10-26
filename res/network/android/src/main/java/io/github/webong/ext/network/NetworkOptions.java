package io.github.webong.ext.network;

public final class NetworkOptions {
    private final int maxInterfaces;
    private final int maxAddresses;
    private final int maxRoutes;
    public NetworkOptions(int maxInterfaces, int maxAddresses, int maxRoutes) {
        if (maxInterfaces < 1 || maxInterfaces > 65536 || maxAddresses < 1 || maxAddresses > 65536
                || maxRoutes < 1 || maxRoutes > 65536) throw new IllegalArgumentException("network limits exceed the supported range");
        this.maxInterfaces = maxInterfaces; this.maxAddresses = maxAddresses; this.maxRoutes = maxRoutes;
    }
    public NetworkOptions() { this(256, 256, 4096); }
    public int maxInterfaces() { return maxInterfaces; }
    public int maxAddresses() { return maxAddresses; }
    public int maxRoutes() { return maxRoutes; }
}
