import XCTest
@testable import ExtSystem

/// The shared time bound (Bound.swift, one copy in each res/system Swift package).
final class BoundTests: XCTestCase {
    func testValidTimeouts() {
        XCTAssertTrue(ObservationBound.valid(1e-9))
        XCTAssertTrue(ObservationBound.valid(3_600))
        for timeout in [0, -1, 3_600.001, .infinity, -.infinity, .nan] as [TimeInterval] {
            XCTAssertFalse(ObservationBound.valid(timeout), "\(timeout)")
        }
    }

    func testStopsAtTheDeadline() {
        XCTAssertNil(ObservationBound(timeout: 60).stop)
        XCTAssertGreaterThan(ObservationBound(timeout: 60).remaining, 59)
        XCTAssertEqual(ObservationBound(timeout: 1e-9).stop, .timeout)
        XCTAssertEqual(ObservationBound(timeout: 1e-9).remaining, 0)
    }

    func testRaceReturnsTheResult() async throws {
        let value = try await ObservationBound(timeout: 5).race { 42 }
        XCTAssertEqual(value, 42)
        do {
            _ = try await ObservationBound(timeout: 5).race { () async throws -> Int in throw URLError(.badURL) }
            XCTFail("no error")
        } catch let error as URLError {
            XCTAssertEqual(error.code, .badURL)
        }
    }

    func testRaceAbandonsACallThatNeverAnswers() async throws {
        let started = Date()
        do {
            _ = try await ObservationBound(timeout: 0.05).race { () async throws -> Int in
                await withCheckedContinuation { (_: CheckedContinuation<Void, Never>) in } // never resumed
                return 1
            }
            XCTFail("no timeout")
        } catch let stopped as ObservationBound.Stopped {
            XCTAssertEqual(stopped.stop, .timeout)
        }
        XCTAssertLessThan(Date().timeIntervalSince(started), 2)
    }

    func testRaceIsCanceledWithItsTask() async throws {
        let task = Task { () -> Error? in
            do {
                _ = try await ObservationBound(timeout: 60).race { () async throws -> Int in
                    try await Task.sleep(nanoseconds: 30_000_000_000)
                    return 1
                }
                return nil
            } catch { return error }
        }
        try await Task.sleep(nanoseconds: 20_000_000)
        task.cancel()
        let error = await task.value
        XCTAssertEqual(error as? ObservationBound.Stopped, ObservationBound.Stopped(stop: .canceled))
    }

    func testRaceOfACanceledTaskDoesNotStart() async throws {
        let task = Task { () -> Error? in
            while !Task.isCancelled { await Task.yield() }
            do { _ = try await ObservationBound(timeout: 60).race { 1 }; return nil } catch { return error }
        }
        task.cancel()
        let error = await task.value
        XCTAssertEqual(error as? ObservationBound.Stopped, ObservationBound.Stopped(stop: .canceled))
    }
}
