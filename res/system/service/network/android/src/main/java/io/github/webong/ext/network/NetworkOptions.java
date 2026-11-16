package io.github.webong.ext.network;

/**
 * Bounds a network observation: 256 interfaces, 256 addresses per interface, 4096 routes and five seconds
 * by default, as Go's network.Options.
 */
public final class NetworkOptions {
    /** Go's default deadline. */
    public static final long DEFAULT_TIMEOUT_MILLIS = 5_000;
    private final int maxInterfaces;
    private final int maxAddresses;
    private final int maxRoutes;
    private final long timeoutMillis;
    public NetworkOptions(int maxInterfaces, int maxAddresses, int maxRoutes) {
        this(maxInterfaces, maxAddresses, maxRoutes, DEFAULT_TIMEOUT_MILLIS);
    }
    /** @throws IllegalArgumentException when a limit is outside 1 to 65536 or the timeout is not positive */
    public NetworkOptions(int maxInterfaces, int maxAddresses, int maxRoutes, long timeoutMillis) {
        if (maxInterfaces < 1 || maxInterfaces > 65536 || maxAddresses < 1 || maxAddresses > 65536
                || maxRoutes < 1 || maxRoutes > 65536) throw new IllegalArgumentException("network limits exceed the supported range");
        if (timeoutMillis < 1) throw new IllegalArgumentException("network timeout must be positive");
        this.maxInterfaces = maxInterfaces; this.maxAddresses = maxAddresses; this.maxRoutes = maxRoutes;
        this.timeoutMillis = timeoutMillis;
    }
    public NetworkOptions() { this(256, 256, 4096); }
    public int maxInterfaces() { return maxInterfaces; }
    public int maxAddresses() { return maxAddresses; }
    public int maxRoutes() { return maxRoutes; }
    public long timeoutMillis() { return timeoutMillis; }
}
