package io.github.webong.ext.network;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

public final class NetworkInterfaceInfo {
    private final int index;
    private final String name;
    private final int mtu;
    private final String hardwareAddress;
    private final List<String> flags;
    private final List<NetworkAddress> addresses;

    public NetworkInterfaceInfo(int index, String name, int mtu, String hardwareAddress,
            List<String> flags, List<NetworkAddress> addresses) {
        if (index < 0 || name == null || name.isEmpty()) throw new IllegalArgumentException("network interface identity is required");
        this.index = index; this.name = name; this.mtu = mtu; this.hardwareAddress = hardwareAddress;
        this.flags = Collections.unmodifiableList(new ArrayList<>(flags));
        this.addresses = Collections.unmodifiableList(new ArrayList<>(addresses));
    }
    public int index() { return index; }
    public String name() { return name; }
    public int mtu() { return mtu; }
    public String hardwareAddress() { return hardwareAddress; }
    public List<String> flags() { return flags; }
    public List<NetworkAddress> addresses() { return addresses; }
}
