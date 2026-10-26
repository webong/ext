package io.github.webong.ext.network;

import java.util.Collections;

public final class NetworkTests {
    private NetworkTests() {}
    public static void main(String[] args) {
        boolean refused = false;
        try { new NetworkOptions(0, 1, 1); } catch (IllegalArgumentException expected) { refused = true; }
        assert refused;
        NetworkAddress address = new NetworkAddress("127.0.0.1", 8, "ipv4", "loopback");
        NetworkInterfaceInfo item = new NetworkInterfaceInfo(1, "lo", 65536, null,
                Collections.singletonList("loopback"), Collections.singletonList(address));
        NetworkSnapshot snapshot = new NetworkSnapshot("fixture", "2026-01-01T00:00:00Z",
                Collections.singletonList(item), Collections.emptyList(),
                new NetworkResolver(Collections.emptyList(), Collections.emptyList(), Collections.emptyList()),
                new NetworkCollectionStatus("complete", null, "2026-01-01T00:00:00Z"),
                new NetworkCollectionStatus("unsupported", null, "2026-01-01T00:00:00Z"),
                new NetworkCollectionStatus("unsupported", null, "2026-01-01T00:00:00Z"));
        assert snapshot.interfaces().size() == 1;
        assert snapshot.interfaces().get(0).addresses().get(0).scope().equals("loopback");
    }
}
