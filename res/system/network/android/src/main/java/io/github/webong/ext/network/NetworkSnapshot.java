package io.github.webong.ext.network;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class NetworkSnapshot {
    private final String platform;
    private final String observedAt;
    private final List<NetworkInterfaceInfo> interfaces;
    private final List<NetworkRoute> routes;
    private final NetworkResolver resolver;
    private final NetworkCollectionStatus interfaceStatus;
    private final NetworkCollectionStatus routeStatus;
    private final NetworkCollectionStatus resolverStatus;

    public NetworkSnapshot(String platform, String observedAt, List<NetworkInterfaceInfo> interfaces,
            List<NetworkRoute> routes, NetworkResolver resolver, NetworkCollectionStatus interfaceStatus,
            NetworkCollectionStatus routeStatus, NetworkCollectionStatus resolverStatus) {
        if (platform == null || platform.isEmpty() || observedAt == null || observedAt.isEmpty()
                || resolver == null || interfaceStatus == null || routeStatus == null || resolverStatus == null) {
            throw new IllegalArgumentException("network snapshot fields are required");
        }
        this.platform = platform; this.observedAt = observedAt;
        this.interfaces = Collections.unmodifiableList(new ArrayList<>(interfaces));
        this.routes = Collections.unmodifiableList(new ArrayList<>(routes)); this.resolver = resolver;
        this.interfaceStatus = interfaceStatus; this.routeStatus = routeStatus; this.resolverStatus = resolverStatus;
    }
    public String platform() { return platform; }
    public String observedAt() { return observedAt; }
    public List<NetworkInterfaceInfo> interfaces() { return interfaces; }
    public List<NetworkRoute> routes() { return routes; }
    public NetworkResolver resolver() { return resolver; }
    public NetworkCollectionStatus interfaceStatus() { return interfaceStatus; }
    public NetworkCollectionStatus routeStatus() { return routeStatus; }
    public NetworkCollectionStatus resolverStatus() { return resolverStatus; }
}
