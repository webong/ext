import Foundation

/// One progress event of a call: a `progress` count of `completed` out of `total` (zero when
/// not known), or a `log` line, optionally on a stream (`out` or `err`). Text is at most
/// `ProgressEvent.maxTextBytes` bytes of UTF-8; `ProgressEvent.split` cuts a longer line.
public struct ProgressEvent: Equatable, Sendable {
    /// The most text one event may carry, in bytes of UTF-8.
    public static let maxTextBytes = Frames.maxProgressText

    public var kind: String
    public var stream: String
    public var text: String
    public var completed: UInt64
    public var total: UInt64

    public init(kind: String, stream: String = "", text: String = "", completed: UInt64 = 0, total: UInt64 = 0) {
        self.kind = kind
        self.stream = stream
        self.text = text
        self.completed = completed
        self.total = total
    }

    public static func log(_ text: String) -> ProgressEvent { ProgressEvent(kind: "log", text: text) }

    /// A log line of a program's standard output.
    public static func out(_ text: String) -> ProgressEvent { ProgressEvent(kind: "log", stream: "out", text: text) }

    /// A log line of a program's standard error.
    public static func err(_ text: String) -> ProgressEvent { ProgressEvent(kind: "log", stream: "err", text: text) }

    /// `completed` of `total`; zero when the total is not known.
    public static func fraction(_ completed: UInt64, of total: UInt64) -> ProgressEvent {
        ProgressEvent(kind: "progress", completed: completed, total: total)
    }

    /// Cuts text into pieces that each fit one event: at most `maxTextBytes` bytes of UTF-8,
    /// never ending inside a character.
    public static func split(_ text: String) -> [String] {
        var pieces: [String] = []
        var piece = String.UnicodeScalarView()
        var bytes = 0
        for scalar in text.unicodeScalars {
            let size = String(scalar).utf8.count
            if bytes + size > maxTextBytes {
                pieces.append(String(piece))
                piece = String.UnicodeScalarView()
                bytes = 0
            }
            piece.append(scalar)
            bytes += size
        }
        if !piece.isEmpty { pieces.append(String(piece)) }
        return pieces
    }
}

/// The rate at which a call may report progress, as the host set it (defaults filled in).
public struct ProgressLimits: Equatable, Sendable {
    public var eventsPerSecond: Int64
    public var bytesPerSecond: Int64
    public init(eventsPerSecond: Int64, bytesPerSecond: Int64) {
        self.eventsPerSecond = eventsPerSecond
        self.bytesPerSecond = bytesPerSecond
    }
}

/// What became of a progress event the call reported.
public enum ProgressOutcome: Sendable {
    /// Written to the host.
    case sent
    /// Dropped by the rate limit, which is routine: the next event that passes carries the count.
    case dropped
}

/// A progress or reverse-call operation failed locally. A failure of the host's answer is a
/// `RemoteError` instead.
public struct FrameError: Error, Equatable, Sendable, CustomStringConvertible {
    public enum Code: String, Sendable {
        /// No frame channel on this call: the operation did not declare progress, or the transport has none.
        case unsupported
        /// The event or request breaks the rules; nothing was sent.
        case invalid
        /// The call is over, or the connection ended.
        case closed
        /// The reverse call's deadline passed before the host answered.
        case deadline
        /// The waiting thread was cancelled.
        case canceled
        /// The host broke the framing rules; the session ends.
        case `protocol`
    }

    public let code: Code
    public var description: String { code.rawValue }
    init(_ code: Code) { self.code = code }
}

/// Calls an operation the host offers (a declared reverse operation) and waits for its answer.
/// Safe to use from several threads at once.
public protocol ReverseCaller: Sendable {
    /// Calls `operation` of `contract` with a payload (one JSON value, or nil for none) and
    /// returns the host's payload as one JSON value. The call waits until the host answers or
    /// the deadline of the call (or, for a session-scoped caller, 30 seconds) passes.
    ///
    /// - Throws: `RemoteError` when the host refused or failed it (the code says why, for
    ///   example `reverse_undeclared`, `denied`, `busy`, `too_large`, `expired`), or
    ///   `FrameError` when the call could not be made or its answer did not come.
    func call(_ contract: ContractRef, _ operation: String, payload: Data?) throws -> Data

    /// As `call(_:_:payload:)`, giving up after `timeout` seconds (never past the call's own deadline).
    func call(_ contract: ContractRef, _ operation: String, payload: Data?, timeout: TimeInterval) throws -> Data
}
