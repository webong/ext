package io.github.webong.ext.system;

import java.util.List;
import java.util.Objects;

public final class ResourceInfo {
    private final String os;
    private final String architecture;
    private final String observedAt;
    private final int logicalCPUs;
    private final double cpuCapacity;
    private final ResourceCollectionStatus cpuStatus;
    private final long memoryBytes;
    private final long memoryCapacityBytes;
    private final ResourceCollectionStatus memoryStatus;
    private final List<StorageInfo> storage;
    private final PowerInfo power;
    private final ContainerInfo container;
    private final Double uptimeSeconds;
    private final String bootTime;
    private final ResourceCollectionStatus uptimeStatus;

    public ResourceInfo(String os, String architecture, String observedAt, int logicalCPUs, double cpuCapacity,
                        ResourceCollectionStatus cpuStatus, long memoryBytes, long memoryCapacityBytes,
                        ResourceCollectionStatus memoryStatus, List<StorageInfo> storage, PowerInfo power,
                        ContainerInfo container, Double uptimeSeconds, String bootTime,
                        ResourceCollectionStatus uptimeStatus) {
        if (os == null || os.isEmpty() || architecture == null || architecture.isEmpty()) {
            throw new IllegalArgumentException("resource identity is required");
        }
        if (!timestamp(observedAt) || bootTime != null && !timestamp(bootTime)) {
            throw new IllegalArgumentException("resource times must be RFC 3339 timestamps");
        }
        if (logicalCPUs < 1) throw new IllegalArgumentException("logical CPU count must be at least one");
        if (!(cpuCapacity >= 0 && cpuCapacity <= logicalCPUs)) {
            throw new IllegalArgumentException("CPU capacity must be between zero and the logical CPU count");
        }
        if (memoryBytes < 0 || memoryCapacityBytes < 0 || memoryCapacityBytes > memoryBytes) {
            throw new IllegalArgumentException("memory capacity must not exceed memory");
        }
        if (uptimeSeconds != null && !(uptimeSeconds >= 0 && !uptimeSeconds.isInfinite()) || (uptimeSeconds == null) != (bootTime == null)) {
            throw new IllegalArgumentException("uptime and boot time come together, and uptime is not negative");
        }
        if (cpuStatus == null || memoryStatus == null || container == null || uptimeStatus == null) {
            throw new IllegalArgumentException("collection statuses are required");
        }
        if (storage == null) throw new IllegalArgumentException("storage list is required");
        this.os = os;
        this.architecture = architecture;
        this.observedAt = observedAt;
        this.logicalCPUs = logicalCPUs;
        this.cpuCapacity = cpuCapacity;
        this.cpuStatus = cpuStatus;
        this.memoryBytes = memoryBytes;
        this.memoryCapacityBytes = memoryCapacityBytes;
        this.memoryStatus = memoryStatus;
        this.storage = List.copyOf(storage);
        this.power = power;
        this.container = container;
        this.uptimeSeconds = uptimeSeconds;
        this.bootTime = bootTime;
        this.uptimeStatus = uptimeStatus;
    }

    private static final java.util.regex.Pattern TIMESTAMP = java.util.regex.Pattern.compile(
        "\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(\\.\\d+)?(Z|[+-]\\d{2}:\\d{2})");

    private static boolean timestamp(String value) {
        return value != null && TIMESTAMP.matcher(value).matches();
    }

    public String os() { return os; }
    public String architecture() { return architecture; }
    public String observedAt() { return observedAt; }
    public int logicalCPUs() { return logicalCPUs; }
    public double cpuCapacity() { return cpuCapacity; }
    public ResourceCollectionStatus cpuStatus() { return cpuStatus; }
    public long memoryBytes() { return memoryBytes; }
    public long memoryCapacityBytes() { return memoryCapacityBytes; }
    public ResourceCollectionStatus memoryStatus() { return memoryStatus; }
    public List<StorageInfo> storage() { return storage; }
    public PowerInfo power() { return power; }
    public ContainerInfo container() { return container; }
    public Double uptimeSeconds() { return uptimeSeconds; }
    public String bootTime() { return bootTime; }
    public ResourceCollectionStatus uptimeStatus() { return uptimeStatus; }

    @Override public boolean equals(Object other) {
        if (!(other instanceof ResourceInfo)) return false;
        ResourceInfo value = (ResourceInfo) other;
        return os.equals(value.os) && architecture.equals(value.architecture) && observedAt.equals(value.observedAt)
            && logicalCPUs == value.logicalCPUs && Double.compare(cpuCapacity, value.cpuCapacity) == 0
            && cpuStatus.equals(value.cpuStatus) && memoryBytes == value.memoryBytes
            && memoryCapacityBytes == value.memoryCapacityBytes && memoryStatus.equals(value.memoryStatus)
            && storage.equals(value.storage) && Objects.equals(power, value.power) && container.equals(value.container)
            && Objects.equals(uptimeSeconds, value.uptimeSeconds) && Objects.equals(bootTime, value.bootTime)
            && uptimeStatus.equals(value.uptimeStatus);
    }
    @Override public int hashCode() {
        return Objects.hash(os, architecture, observedAt, logicalCPUs, cpuCapacity, cpuStatus, memoryBytes,
            memoryCapacityBytes, memoryStatus, storage, power, container, uptimeSeconds, bootTime, uptimeStatus);
    }
}
