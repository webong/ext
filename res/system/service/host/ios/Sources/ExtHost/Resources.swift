import Darwin
import Foundation

#if os(iOS)
import UIKit
#endif

public struct ResourceCollectionStatus: Codable, Equatable, Sendable {
    public let state: String
    public let detail: String?
    public let observedAt: String?

    public init(state: String, detail: String? = nil, observedAt: String? = nil) {
        self.state = state; self.detail = detail; self.observedAt = observedAt
    }

    enum CodingKeys: String, CodingKey {
        case state, detail
        case observedAt = "observed_at"
    }
}

public struct StorageInfo: Codable, Equatable, Sendable {
    public let device: String
    public let mountPoint: String
    public let totalBytes: UInt64
    public let availableBytes: UInt64
    public let type: String?
    public let readOnly: Bool

    public init(device: String, mountPoint: String, totalBytes: UInt64,
                availableBytes: UInt64, type: String? = nil, readOnly: Bool = false) {
        self.device = device; self.mountPoint = mountPoint; self.totalBytes = totalBytes
        self.availableBytes = availableBytes; self.type = type; self.readOnly = readOnly
    }

    enum CodingKeys: String, CodingKey {
        case device, mountPoint
        case totalBytes = "total_bytes"
        case availableBytes = "available_bytes"
        case type, readOnly = "read_only"
    }
}

public struct PowerInfo: Codable, Equatable, Sendable {
    public let state: String // charging, discharging, charged, unknown
    public let percentage: Int // 0-100
    public let timeRemainingSeconds: Int64?

    public init(state: String, percentage: Int, timeRemainingSeconds: Int64? = nil) {
        self.state = state; self.percentage = percentage; self.timeRemainingSeconds = timeRemainingSeconds
    }

    enum CodingKeys: String, CodingKey {
        case state, percentage
        case timeRemainingSeconds = "time_remaining_seconds"
    }
}

public struct ContainerInfo: Codable, Equatable, Sendable {
    public let detected: Bool
    public let state: String
    public let evidence: [String]

    public init(detected: Bool, state: String, evidence: [String] = []) {
        self.detected = detected; self.state = state; self.evidence = evidence
    }

    enum CodingKeys: String, CodingKey {
        case detected, state, evidence
    }
}

public struct ResourceInfo: Codable, Equatable, Sendable {
    public let os: String
    public let architecture: String
    public let observedAt: String
    public let logicalCPUs: Int
    public let cpuCapacity: Double
    public let cpuStatus: ResourceCollectionStatus
    public let memoryBytes: UInt64
    public let memoryCapacityBytes: UInt64
    public let memoryStatus: ResourceCollectionStatus
    public let storage: [StorageInfo]
    public let power: PowerInfo?
    public let container: ContainerInfo
    public let uptimeSeconds: Double?
    public let bootTime: String?
    public let uptimeStatus: ResourceCollectionStatus

    public init(os: String, architecture: String, observedAt: String,
                logicalCPUs: Int, cpuCapacity: Double, cpuStatus: ResourceCollectionStatus,
                memoryBytes: UInt64, memoryCapacityBytes: UInt64, memoryStatus: ResourceCollectionStatus,
                storage: [StorageInfo], power: PowerInfo?, container: ContainerInfo,
                uptimeSeconds: Double?, bootTime: String?, uptimeStatus: ResourceCollectionStatus) {
        self.os = os; self.architecture = architecture; self.observedAt = observedAt
        self.logicalCPUs = logicalCPUs; self.cpuCapacity = cpuCapacity; self.cpuStatus = cpuStatus
        self.memoryBytes = memoryBytes; self.memoryCapacityBytes = memoryCapacityBytes; self.memoryStatus = memoryStatus
        self.storage = storage; self.power = power; self.container = container
        self.uptimeSeconds = uptimeSeconds; self.bootTime = bootTime; self.uptimeStatus = uptimeStatus
    }

    enum CodingKeys: String, CodingKey {
        case os, architecture
        case observedAt = "observed_at"
        case logicalCPUs = "logical_cpus"
        case cpuCapacity = "cpu_capacity"
        case cpuStatus = "cpu_status"
        case memoryBytes = "memory_bytes"
        case memoryCapacityBytes = "memory_capacity_bytes"
        case memoryStatus = "memory_status"
        case storage, power, container
        case uptimeSeconds = "uptime_seconds"
        case bootTime = "boot_time"
        case uptimeStatus = "uptime_status"
    }
}

public enum ResourceObservationError: Error, Equatable {
    case nativeFailure
}

/// A caller-owned observer for local host resource metadata. All values are
/// read from the local process only: no command is launched, no network is
/// contacted, and no identity is attached. Fields whose native source is
/// unavailable carry a non-complete status instead of guessing.
@MainActor
public final class SystemResourceObserver {
    private static let logicalCPUs: Int = {
        var count: Int32 = 0
        var size = MemoryLayout<Int32>.size
        if sysctlbyname("machdep.cpu.thread_count", &count, &size, nil, 0) == 0, count > 0 {
            return Int(count)
        }
        return ProcessInfo.processInfo.processorCount
    }()

    public init() {}

    public func observe() throws -> ResourceInfo {
        let now = resourceTimestamp(Date())
        let logical = Self.logicalCPUs
        let cpuCapacity = Double(max(logical, 1))
        let memory = platformMemory()
        let storage = platformStorage()
        let power = platformPower()
        let container = platformContainer()
        let uptime = platformUptime()
        return ResourceInfo(
            os: resourcePlatform, architecture: processorArchitecture,
            observedAt: now,
            logicalCPUs: logical, cpuCapacity: cpuCapacity,
            cpuStatus: ResourceCollectionStatus(state: "complete", detail: "logical CPU count", observedAt: now),
            memoryBytes: memory.0, memoryCapacityBytes: memory.1,
            memoryStatus: memory.2,
            storage: storage, power: power, container: container,
            uptimeSeconds: uptime.0, bootTime: uptime.1, uptimeStatus: uptime.2
        )
    }
}

private func resourceTimestamp(_ date: Date) -> String {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter.string(from: date)
}

private var resourcePlatform: String {
#if os(iOS)
    "ios"
#else
    "darwin"
#endif
}

private var processorArchitecture: String {
    var info = utsname()
    uname(&info)
    return withUnsafeBytes(of: &info.machine) { buffer in
        var n = 0
        while n < buffer.count, buffer[n] != 0 { n += 1 }
        return String(decoding: buffer[0..<n], as: UTF8.self)
    }
}

// MARK: - Memory

/// Returns (bytes, capacityBytes, status). On Apple platforms the effective
/// capacity equals the physical capacity; there is no container memory limit
/// exposed to this library.
private func platformMemory() -> (UInt64, UInt64, ResourceCollectionStatus) {
    let now = resourceTimestamp(Date())
    var mib: [Int32] = [CTL_HW, HW_MEMSIZE]
    var size: UInt64 = 0
    var length = MemoryLayout<UInt64>.size
    if sysctl(&mib, 2, &size, &length, nil, 0) == 0, size > 0 {
        return (size, size, ResourceCollectionStatus(state: "complete", detail: "physical memory capacity", observedAt: now))
    }
    return (0, 0, ResourceCollectionStatus(state: "unsupported", detail: "physical memory capacity is unavailable", observedAt: now))
}

// MARK: - Storage

/// Observes the storage volume holding the app's container (iOS) or the root
/// volume (macOS). Only one volume is reported to avoid guessing about other
/// mount points from a sandboxed process.
private func platformStorage() -> [StorageInfo] {
#if os(iOS)
    guard let url = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first else {
        return []
    }
#else
    let url = URL(fileURLWithPath: "/")
#endif
    do {
        let values = try url.resourceValues(forKeys: [
            .volumeTotalCapacityKey, .volumeAvailableCapacityKey, .volumeIsReadOnlyKey
        ])
        let total = values.volumeTotalCapacity ?? 0
        let available = values.volumeAvailableCapacity ?? 0
        let readOnly = values.volumeIsReadOnly ?? false
        return [StorageInfo(
            device: "local", mountPoint: url.path,
            totalBytes: UInt64(total), availableBytes: UInt64(available),
            type: "apfs", readOnly: readOnly
        )]
    } catch {
        return []
    }
}

// MARK: - Power

private func platformPower() -> PowerInfo? {
#if os(iOS)
    let device = UIDevice.current
    device.isBatteryMonitoringEnabled = true
    let level = device.batteryLevel
    if level < 0 {
        return nil
    }
    let percentage = Int(round(level * 100))
    let state: String
    switch device.batteryState {
    case .charging: state = "charging"
    case .full: state = "charged"
    case .unplugged: state = "discharging"
    default: state = "unknown"
    }
    return PowerInfo(state: state, percentage: max(0, min(100, percentage)))
#else
    // macOS power sources require IOKit iteration; report unavailable rather
    // than guessing.
    return nil
#endif
}

// MARK: - Container

private func platformContainer() -> ContainerInfo {
    // Apple platforms do not expose a stable local container marker to this
    // library; container detection is unsupported rather than guessed.
    return ContainerInfo(detected: false, state: "unsupported", evidence: [])
}

// MARK: - Uptime

private func platformUptime() -> (Double?, String?, ResourceCollectionStatus) {
    let now = resourceTimestamp(Date())
    var bootTime: timeval = timeval()
    var length = MemoryLayout<timeval>.size
    var mib: [Int32] = [CTL_KERN, KERN_BOOTTIME]
    if sysctl(&mib, 2, &bootTime, &length, nil, 0) == 0, bootTime.tv_sec > 0 {
        let boot = Date(timeIntervalSince1970: TimeInterval(bootTime.tv_sec) + TimeInterval(bootTime.tv_usec) / 1_000_000)
        let uptime = Date().timeIntervalSince(boot)
        return (uptime, resourceTimestamp(boot), ResourceCollectionStatus(state: "complete", observedAt: now))
    }
    return (nil, nil, ResourceCollectionStatus(state: "unsupported", detail: "boot time is unavailable", observedAt: now))
}
