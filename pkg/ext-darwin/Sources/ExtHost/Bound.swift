import Dispatch
import Foundation

/// Deadline and task-cancellation check around one synchronous C ledger call.
struct ObservationBound {
    enum Stop: Equatable { case timeout, canceled }
    static let maximumTimeout: TimeInterval = 3_600
    private let deadline: UInt64

    static func valid(_ timeout: TimeInterval) -> Bool {
        timeout.isFinite && timeout > 0 && timeout <= maximumTimeout
    }

    init(timeout: TimeInterval) {
        let nanoseconds = UInt64((min(max(timeout, 1e-9), Self.maximumTimeout) * 1_000_000_000).rounded(.up))
        deadline = DispatchTime.now().uptimeNanoseconds &+ (nanoseconds - 1)
    }

    var stop: Stop? {
        if Task.isCancelled { return .canceled }
        if DispatchTime.now().uptimeNanoseconds >= deadline { return .timeout }
        return nil
    }
}
