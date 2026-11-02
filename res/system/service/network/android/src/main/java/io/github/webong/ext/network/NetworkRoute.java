package io.github.webong.ext.network;

public final class NetworkRoute {
    private final String destination;
    private final String gateway;
    private final String interfaceName;
    private final int metric;
    private final boolean defaultRoute;

    public NetworkRoute(String destination, String gateway, String interfaceName, int metric, boolean defaultRoute) {
        if (destination == null || destination.isEmpty()) throw new IllegalArgumentException("route destination is required");
        this.destination = destination; this.gateway = gateway; this.interfaceName = interfaceName;
        this.metric = metric; this.defaultRoute = defaultRoute;
    }
    public String destination() { return destination; }
    public String gateway() { return gateway; }
    public String interfaceName() { return interfaceName; }
    public int metric() { return metric; }
    public boolean defaultRoute() { return defaultRoute; }
}
