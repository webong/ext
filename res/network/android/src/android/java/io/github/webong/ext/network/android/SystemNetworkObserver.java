package io.github.webong.ext.network.android;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.IpPrefix;
import android.net.LinkProperties;
import android.net.Network;
import android.net.RouteInfo;
import io.github.webong.ext.network.NetworkAddress;
import io.github.webong.ext.network.NetworkCollectionStatus;
import io.github.webong.ext.network.NetworkInterfaceInfo;
import io.github.webong.ext.network.NetworkOptions;
import io.github.webong.ext.network.NetworkResolver;
import io.github.webong.ext.network.NetworkRoute;
import io.github.webong.ext.network.NetworkSnapshot;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.net.SocketException;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.Date;
import java.util.Enumeration;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.TimeZone;

/** Observes the Android device's local interfaces and ConnectivityManager link properties. */
public final class SystemNetworkObserver {
    private final NetworkOptions options;
    public SystemNetworkObserver(NetworkOptions options) { this.options = options; }
    public SystemNetworkObserver() { this(new NetworkOptions()); }

    @SuppressWarnings("deprecation")
    public NetworkSnapshot observe(Context context) {
        String now = timestamp();
        List<NetworkInterfaceInfo> interfaces = new ArrayList<>();
        boolean interfacePartial = false;
        try {
            Enumeration<NetworkInterface> values = NetworkInterface.getNetworkInterfaces();
            while (values != null && values.hasMoreElements()) {
                NetworkInterface value = values.nextElement();
                if (interfaces.size() >= options.maxInterfaces()) { interfacePartial = true; break; }
                List<NetworkAddress> addresses = new ArrayList<>();
                for (InterfaceAddress assigned : value.getInterfaceAddresses()) {
                    if (addresses.size() >= options.maxAddresses()) { interfacePartial = true; break; }
                    InetAddress address = assigned.getAddress();
                    int prefixLength = Math.max(0, assigned.getNetworkPrefixLength());
                    addresses.add(new NetworkAddress(cleanAddress(address), prefixLength,
                            address instanceof Inet4Address ? "ipv4" : "ipv6", scope(address)));
                }
                addresses.sort(Comparator.comparing(NetworkAddress::ip));
                interfaces.add(new NetworkInterfaceInfo(value.getIndex(), value.getName(), value.getMTU(),
                        hardwareAddress(value), flags(value), addresses));
            }
        } catch (SocketException exception) { interfacePartial = true; }
        interfaces.sort(Comparator.comparingInt(NetworkInterfaceInfo::index));

        List<NetworkRoute> routes = new ArrayList<>();
        Set<String> dns = new LinkedHashSet<>();
        NetworkCollectionStatus routeStatus;
        NetworkCollectionStatus resolverStatus;
        try {
            ConnectivityManager manager = (ConnectivityManager) context.getSystemService(Context.CONNECTIVITY_SERVICE);
            if (manager == null) throw new IllegalStateException("ConnectivityManager is unavailable");
            boolean routePartial = false;
            for (Network network : manager.getAllNetworks()) {
                LinkProperties links = manager.getLinkProperties(network);
                if (links == null) continue;
                for (InetAddress server : links.getDnsServers()) dns.add(cleanAddress(server));
                for (RouteInfo route : links.getRoutes()) {
                    if (routes.size() >= options.maxRoutes()) { routePartial = true; break; }
                    IpPrefix destination = route.getDestination();
                    InetAddress gateway = route.getGateway();
                    routes.add(new NetworkRoute(destination.toString(), gateway == null || gateway.isAnyLocalAddress() ? null : cleanAddress(gateway),
                            links.getInterfaceName(), 0, route.isDefaultRoute()));
                }
            }
            routes.sort(Comparator.comparing(NetworkRoute::destination).thenComparing(route -> route.interfaceName() == null ? "" : route.interfaceName()));
            routeStatus = new NetworkCollectionStatus(routePartial ? "partial" : "complete", routePartial ? "route limit reached" : null, now);
            resolverStatus = new NetworkCollectionStatus("complete", null, now);
        } catch (SecurityException exception) {
            routeStatus = new NetworkCollectionStatus("permission-denied", "ACCESS_NETWORK_STATE is not granted", now);
            resolverStatus = new NetworkCollectionStatus("permission-denied", "ACCESS_NETWORK_STATE is not granted", now);
        } catch (RuntimeException exception) {
            routeStatus = new NetworkCollectionStatus("unavailable", "Android link properties are unavailable", now);
            resolverStatus = new NetworkCollectionStatus("unavailable", "Android resolver properties are unavailable", now);
        }
        return new NetworkSnapshot("android", now, interfaces, routes,
                new NetworkResolver(new ArrayList<>(dns), Collections.emptyList(), Collections.emptyList()),
                new NetworkCollectionStatus(interfacePartial ? "partial" : "complete",
                        interfacePartial ? "interface or address limit reached, or an interface could not be inspected" : null, now),
                routeStatus, resolverStatus);
    }

    private static String cleanAddress(InetAddress address) {
        String value = address.getHostAddress();
        int zone = value.indexOf('%');
        return zone < 0 ? value : value.substring(0, zone);
    }
    private static String scope(InetAddress value) {
        if (value.isLoopbackAddress()) return "loopback";
        if (value.isLinkLocalAddress()) return "link";
        if (value.isSiteLocalAddress()) return "private";
        if (value.isMulticastAddress()) return "multicast";
        if (value.isAnyLocalAddress()) return "unspecified";
        return "global";
    }
    private static String hardwareAddress(NetworkInterface value) {
        try {
            byte[] bytes = value.getHardwareAddress(); if (bytes == null) return null;
            StringBuilder result = new StringBuilder();
            for (byte item : bytes) { if (result.length() > 0) result.append(':'); result.append(String.format(Locale.ROOT, "%02x", item & 0xff)); }
            return result.toString();
        } catch (SocketException ignored) { return null; }
    }
    private static List<String> flags(NetworkInterface value) {
        List<String> result = new ArrayList<>();
        try {
            if (value.isUp()) result.add("up"); if (value.isLoopback()) result.add("loopback");
            if (value.isPointToPoint()) result.add("point-to-point"); if (value.supportsMulticast()) result.add("multicast");
        } catch (SocketException ignored) { }
        Collections.sort(result); return result;
    }
    private static String timestamp() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT);
        format.setTimeZone(TimeZone.getTimeZone("UTC")); return format.format(new Date());
    }
}
