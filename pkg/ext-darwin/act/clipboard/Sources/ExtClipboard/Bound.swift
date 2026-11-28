import Dispatch
import Foundation

/// The time bound and cancellation of one call (docs/adr-architecture.md rule 4), as Go's context
/// deadline: the call stops at its deadline, and when the task that runs it is canceled. Each
/// package of src/ctx/system keeps its own copy, since the Swift targets share no module.
struct ObservationBound {
    /// Why a call stopped early: the codes `timeout` and `canceled` of `testdata/codes.json`.
    enum Stop: Equatable { case timeout, canceled }

    /// The default deadline, as Go's observers: five seconds.
    static let defaultTimeout: TimeInterval = 5
    /// The longest deadline a caller may select: one hour.
    static let maximumTimeout: TimeInterval = 3_600

    private let deadline: UInt64

    /// Whether a caller-selected timeout is a bound: finite, above zero and at most an hour.
    static func valid(_ timeout: TimeInterval) -> Bool {
        timeout.isFinite && timeout > 0 && timeout <= maximumTimeout
    }

    /// The deadline is the last nanosecond inside the timeout, so the clock's resolution never lets
    /// a call run past it: a one-nanosecond timeout has passed by the first check.
    init(timeout: TimeInterval) {
        let nanoseconds = UInt64((min(max(timeout, 1e-9), Self.maximumTimeout) * 1_000_000_000).rounded(.up))
        deadline = DispatchTime.now().uptimeNanoseconds &+ (nanoseconds - 1)
    }

    /// Canceled when the running task is, else timeout once the deadline has passed, else nil.
    /// Cancellation is judged first, as Go reports the parent context's error.
    var stop: Stop? {
        if Task.isCancelled { return .canceled }
        if DispatchTime.now().uptimeNanoseconds >= deadline { return .timeout }
        return nil
    }

    /// The seconds left before the deadline, at least zero.
    var remaining: TimeInterval {
        let now = DispatchTime.now().uptimeNanoseconds
        return now >= deadline ? 0 : TimeInterval(deadline - now) / 1_000_000_000
    }
}

extension ObservationBound {
    /// The error `race` throws when the bound stops first; the caller maps it to its own error.
    struct Stopped: Error, Equatable { let stop: Stop }

    /// Runs `body` until it returns, the deadline passes or the calling task is canceled, whichever
    /// comes first, and throws `Stopped` in the last two cases. A native call that never answers is
    /// abandoned rather than awaited: its task is canceled and a late result is dropped.
    func race<T>(_ body: @escaping () async throws -> T) async throws -> T {
        if let stop { throw Stopped(stop: stop) }
        let race = Race<T>()
        let nanoseconds = UInt64((remaining * 1_000_000_000).rounded(.up))
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<T, Error>) in
                race.start(continuation,
                    work: Task {
                        do { race.finish(.success(try await body())) } catch { race.finish(.failure(error)) }
                    },
                    timer: Task {
                        try? await Task.sleep(nanoseconds: nanoseconds)
                        if !Task.isCancelled { race.finish(.failure(Stopped(stop: .timeout))) }
                    })
            }
        } onCancel: {
            race.finish(.failure(Stopped(stop: .canceled)))
        }
    }
}

/// The one result of a `race`: whichever of the work, the timer and cancellation finishes first
/// resumes the caller, and the other two are canceled or dropped.
private final class Race<T>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<T, Error>?
    private var work: Task<Void, Never>?
    private var timer: Task<Void, Never>?
    private var result: Result<T, Error>?
    private var resumed = false

    func start(_ continuation: CheckedContinuation<T, Error>, work: Task<Void, Never>, timer: Task<Void, Never>) {
        lock.lock()
        guard let result else {
            self.continuation = continuation
            self.work = work
            self.timer = timer
            lock.unlock()
            return
        }
        resumed = true
        lock.unlock()
        work.cancel()
        timer.cancel()
        continuation.resume(with: result)
    }

    func finish(_ value: Result<T, Error>) {
        lock.lock()
        guard !resumed, result == nil else { lock.unlock(); return }
        guard let continuation else {
            // Canceled before the race started: start resumes with this.
            result = value
            lock.unlock()
            return
        }
        resumed = true
        let work = self.work, timer = self.timer
        self.continuation = nil; self.work = nil; self.timer = nil
        lock.unlock()
        work?.cancel()
        timer?.cancel()
        continuation.resume(with: value)
    }
}
