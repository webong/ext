import CExtLedger
import Foundation

/// A state (complete, partial, permission-denied, unsupported or unavailable) and, where something
/// is missing, why: unsupported, permission, no_device or tool_missing (`testdata/capability/v1`).
public struct ResourceCollectionStatus: Codable, Equatable, Sendable {
    public let state: String
    public let reason: String?
    public let detail: String?
    public let observedAt: String?

    public init(state: String, reason: String? = nil, detail: String? = nil, observedAt: String? = nil) {
        self.state = state; self.reason = reason; self.detail = detail; self.observedAt = observedAt
    }

    enum CodingKeys: String, CodingKey {
        case state, reason, detail
        case observedAt = "observed_at"
    }

    /// Checks that the state is known and the reason fits it: none for complete and partial,
    /// permission or none for permission-denied, unsupported or tool_missing for unsupported, and
    /// none, permission, no_device or tool_missing for unavailable (a failed native read has none).
    public func validate() throws {
        let allowed: [String?]
        switch state {
        case "complete", "partial": allowed = [nil]
        case "permission-denied": allowed = [nil, "permission"]
        case "unsupported": allowed = ["unsupported", "tool_missing"]
        case "unavailable": allowed = [nil, "permission", "no_device", "tool_missing"]
        default: throw ResourceObservationError.invalidValue
        }
        guard allowed.contains(where: { $0 == reason }) else { throw ResourceObservationError.invalidValue }
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
        case device
        case mountPoint = "mount_point"
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
    case invalidValue
    /// The observation ran past its timeout (code `timeout`).
    case timeout
    /// The task running the observation was canceled (code `canceled`).
    case canceled

    /// Throws the error of a bound that has stopped.
    static func check(_ bound: ObservationBound) throws {
        switch bound.stop {
        case .timeout?: throw ResourceObservationError.timeout
        case .canceled?: throw ResourceObservationError.canceled
        case nil: return
        }
    }

    /// The stable error code of `res/system/testdata/codes.json`.
    public var code: String {
        switch self {
        case .nativeFailure: return "io"
        case .invalidValue: return "invalid"
        case .timeout: return "timeout"
        case .canceled: return "canceled"
        }
    }
}

extension StorageInfo {
    /// Reads a volume in its wire form: a missing `read_only` is false (Go leaves it out when false).
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(device: try c.decode(String.self, forKey: .device), mountPoint: try c.decode(String.self, forKey: .mountPoint),
                  totalBytes: try c.decode(UInt64.self, forKey: .totalBytes), availableBytes: try c.decode(UInt64.self, forKey: .availableBytes),
                  type: try c.decodeIfPresent(String.self, forKey: .type),
                  readOnly: try c.decodeIfPresent(Bool.self, forKey: .readOnly) ?? false)
    }
}

extension ResourceInfo {
    /// Reads a snapshot in its wire form (`testdata/host/v1`): a missing `storage` is empty.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(
            os: try c.decode(String.self, forKey: .os), architecture: try c.decode(String.self, forKey: .architecture),
            observedAt: try c.decode(String.self, forKey: .observedAt),
            logicalCPUs: try c.decode(Int.self, forKey: .logicalCPUs), cpuCapacity: try c.decode(Double.self, forKey: .cpuCapacity),
            cpuStatus: try c.decode(ResourceCollectionStatus.self, forKey: .cpuStatus),
            memoryBytes: try c.decode(UInt64.self, forKey: .memoryBytes), memoryCapacityBytes: try c.decode(UInt64.self, forKey: .memoryCapacityBytes),
            memoryStatus: try c.decode(ResourceCollectionStatus.self, forKey: .memoryStatus),
            storage: try c.decodeIfPresent([StorageInfo].self, forKey: .storage) ?? [],
            power: try c.decodeIfPresent(PowerInfo.self, forKey: .power),
            container: try c.decode(ContainerInfo.self, forKey: .container),
            uptimeSeconds: try c.decodeIfPresent(Double.self, forKey: .uptimeSeconds),
            bootTime: try c.decodeIfPresent(String.self, forKey: .bootTime),
            uptimeStatus: try c.decode(ResourceCollectionStatus.self, forKey: .uptimeStatus))
    }

    /// Checks a snapshot against the rules of `testdata/host/v1`: the identity and times are set,
    /// the CPU and memory figures agree, each status fits the capability rules, storage, power and
    /// container are consistent, and uptime and boot time come together.
    public func validate() throws {
        func timestamp(_ value: String) -> Bool {
            value.range(of: #"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$"#, options: .regularExpression) != nil
        }
        guard !os.isEmpty, !architecture.isEmpty, timestamp(observedAt), bootTime.map(timestamp) ?? true,
              logicalCPUs >= 1, cpuCapacity >= 0, cpuCapacity <= Double(logicalCPUs),
              memoryCapacityBytes <= memoryBytes else { throw ResourceObservationError.invalidValue }
        for status in [cpuStatus, memoryStatus, uptimeStatus] { try status.validate() }
        for volume in storage where volume.mountPoint.isEmpty || volume.availableBytes > volume.totalBytes {
            throw ResourceObservationError.invalidValue
        }
        if let power {
            guard ["charging", "discharging", "charged", "unknown"].contains(power.state), (0...100).contains(power.percentage),
                  (power.timeRemainingSeconds ?? 0) >= 0 else { throw ResourceObservationError.invalidValue }
        }
        guard ["detected", "none", "unsupported"].contains(container.state),
              container.detected == (container.state == "detected"), container.detected == !container.evidence.isEmpty else {
            throw ResourceObservationError.invalidValue
        }
        var previous: [UInt8]?
        for name in container.evidence {
            let bytes = Array(name.utf8)
            guard !bytes.isEmpty, previous.map({ $0.lexicographicallyPrecedes(bytes) }) ?? true else { throw ResourceObservationError.invalidValue }
            previous = bytes
        }
        guard (uptimeSeconds == nil) == (bootTime == nil), (uptimeSeconds ?? 0) >= 0, uptimeSeconds?.isFinite ?? true else {
            throw ResourceObservationError.invalidValue
        }
    }
}

/// Binds the C ledger's cross-platform host resource collector. This layer only
/// applies the caller's time bound and decodes the shared wire record.
@MainActor
public final class SystemResourceObserver {
    public let timeout: TimeInterval

    public init(timeout: TimeInterval = 5) throws {
        guard ObservationBound.valid(timeout) else { throw ResourceObservationError.invalidValue }
        self.timeout = timeout
    }

    public func observe() throws -> ResourceInfo {
        let bound = ObservationBound(timeout: timeout)
        try ResourceObservationError.check(bound)
        var context = ext_ledger_context(
            canceled: 0,
            deadline_unix_ms: Int64((Date().timeIntervalSince1970 + timeout) * 1_000)
        )
        var output: UnsafeMutablePointer<CChar>?
        var length = 0
        let code = ext_ledger_collect(&context, "resources", &output, &length)
        defer { if let output { ext_ledger_free(output) } }
        switch code {
        case Int32(EXT_LEDGER_OK.rawValue): break
        case Int32(EXT_LEDGER_TIMEOUT.rawValue): throw ResourceObservationError.timeout
        case Int32(EXT_LEDGER_CANCELED.rawValue): throw ResourceObservationError.canceled
        default: throw ResourceObservationError.nativeFailure
        }
        try ResourceObservationError.check(bound)
        guard let output, length > 0 else { throw ResourceObservationError.nativeFailure }
        let value: ResourceInfo
        do {
            value = try JSONDecoder().decode(ResourceInfo.self, from: Data(bytes: output, count: length))
            try value.validate()
        } catch {
            throw ResourceObservationError.invalidValue
        }
        return value
    }
}
