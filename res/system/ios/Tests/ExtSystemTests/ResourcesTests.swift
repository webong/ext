import XCTest
@testable import ExtSystem

final class ResourcesTests: XCTestCase {
    func testWireNamesAndStatuses() throws {
        let now = "1970-01-01T00:16:40.000Z"
        let value = ResourceInfo(
            os: "fixture", architecture: "fixture-arch", observedAt: now,
            logicalCPUs: 4, cpuCapacity: 4.0,
            cpuStatus: ResourceCollectionStatus(state: "complete", detail: "logical CPU count", observedAt: now),
            memoryBytes: 8_589_934_592, memoryCapacityBytes: 8_589_934_592,
            memoryStatus: ResourceCollectionStatus(state: "complete", observedAt: now),
            storage: [StorageInfo(device: "local", mountPoint: "/", totalBytes: 1_000_000_000_000,
                                  availableBytes: 500_000_000_000, type: "apfs", readOnly: false)],
            power: PowerInfo(state: "charging", percentage: 75, timeRemainingSeconds: 3600),
            container: ContainerInfo(detected: false, state: "unsupported"),
            uptimeSeconds: 3600.5, bootTime: now,
            uptimeStatus: ResourceCollectionStatus(state: "complete", observedAt: now)
        )
        let data = try JSONEncoder().encode(value)
        let encoded = String(decoding: data, as: UTF8.self)
        XCTAssertTrue(encoded.contains("\"logical_cpus\""))
        XCTAssertTrue(encoded.contains("\"cpu_capacity\""))
        XCTAssertTrue(encoded.contains("\"memory_capacity_bytes\""))
        XCTAssertTrue(encoded.contains("\"total_bytes\""))
        XCTAssertTrue(encoded.contains("\"available_bytes\""))
        XCTAssertTrue(encoded.contains("\"time_remaining_seconds\""))
        XCTAssertTrue(encoded.contains("\"uptime_seconds\""))
        XCTAssertTrue(encoded.contains("\"boot_time\""))
        XCTAssertTrue(encoded.contains("\"read_only\""))
        XCTAssertFalse(encoded.contains("process_start_id"))
        XCTAssertEqual(try JSONDecoder().decode(ResourceInfo.self, from: data), value)
    }

    @MainActor
    func testNativeObservationReturnsBoundedLocalState() throws {
        let value = try SystemResourceObserver().observe()
        XCTAssertFalse(value.os.isEmpty)
        XCTAssertFalse(value.architecture.isEmpty)
        XCTAssertGreaterThan(value.logicalCPUs, 0)
        XCTAssertGreaterThan(value.cpuCapacity, 0)
        XCTAssertEqual(value.cpuStatus.state, "complete")
        XCTAssertEqual(value.memoryStatus.state, "complete")
        XCTAssertGreaterThan(value.memoryBytes, 0)
        XCTAssertEqual(value.memoryBytes, value.memoryCapacityBytes)
        XCTAssertEqual(value.storage.count, 1)
        XCTAssertEqual(value.storage[0].mountPoint, "/")
        XCTAssertEqual(value.container.state, "unsupported")
        XCTAssertFalse(value.uptimeStatus.state.isEmpty)
        let data = try JSONEncoder().encode(value)
        XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("credential"))
    }
}
