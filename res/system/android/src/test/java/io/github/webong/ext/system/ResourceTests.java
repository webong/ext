package io.github.webong.ext.system;

import java.util.List;

public final class ResourceTests {
    public static void main(String[] arguments) {
        // Constructor validation.
        boolean rejected = false;
        try { new ResourceInfo(null, "arch", "now", 1, 1.0, status("complete"), 0, 0, status("complete"), List.of(), null, container(), null, null, status("complete")); }
        catch (IllegalArgumentException expected) { rejected = true; }
        assert rejected;

        rejected = false;
        try { new ResourceInfo("android", "arm64", "now", 1, 1.0, status("complete"), 0, 0, status("complete"), List.of(), new PowerInfo("charging", 101, null), container(), null, null, status("complete")); }
        catch (IllegalArgumentException expected) { rejected = true; }
        assert rejected;

        // Equality and accessors.
        ResourceInfo first = new ResourceInfo("android", "arm64", "2026-10-28T00:00:00.000Z", 4, 4.0,
                status("complete"), 2_000_000_000L, 8_000_000_000L, status("complete"),
                List.of(new StorageInfo("local", "/data", 1_000_000_000L, 500_000_000L, "ext4", false)),
                new PowerInfo("charging", 75, 3600L), container(), 1234.5, "2026-10-28T00:00:00.000Z", status("complete"));
        assert first.os().equals("android");
        assert first.architecture().equals("arm64");
        assert first.logicalCPUs() == 4;
        assert first.cpuCapacity() == 4.0;
        assert first.memoryBytes() == 2_000_000_000L;
        assert first.memoryCapacityBytes() == 8_000_000_000L;
        assert first.storage().size() == 1;
        assert first.storage().get(0).mountPoint().equals("/data");
        assert first.power().state().equals("charging");
        assert first.power().percentage() == 75;
        assert first.power().timeRemainingSeconds() == 3600L;
        assert first.container().state().equals("unsupported");
        assert !first.container().detected();
        assert first.uptimeSeconds() == 1234.5;
        assert first.bootTime().equals("2026-10-28T00:00:00.000Z");

        ResourceInfo copy = new ResourceInfo("android", "arm64", "2026-10-28T00:00:00.000Z", 4, 4.0,
                status("complete"), 2_000_000_000L, 8_000_000_000L, status("complete"),
                List.of(new StorageInfo("local", "/data", 1_000_000_000L, 500_000_000L, "ext4", false)),
                new PowerInfo("charging", 75, 3600L), container(), 1234.5, "2026-10-28T00:00:00.000Z", status("complete"));
        assert first.equals(copy);
        assert first.hashCode() == copy.hashCode();

        ResourceInfo different = new ResourceInfo("android", "arm64", "2026-10-28T00:00:00.000Z", 4, 4.0,
                status("complete"), 2_000_000_000L, 8_000_000_000L, status("complete"),
                List.of(new StorageInfo("local", "/data", 1_000_000_000L, 500_000_000L, "ext4", false)),
                new PowerInfo("discharging", 75, 3600L), container(), 1234.5, "2026-10-28T00:00:00.000Z", status("complete"));
        assert !first.equals(different);
    }

    private static ResourceCollectionStatus status(String state) {
        return new ResourceCollectionStatus(state, null, "2026-10-28T00:00:00.000Z");
    }

    private static ContainerInfo container() {
        return new ContainerInfo(false, "unsupported", List.of());
    }

    private ResourceTests() {}
}
