import Foundation

/// Progress events and reverse calls (docs/plugin-guest-frames.md). They need a connection that
/// carries frames, which `JSONLine` provides; on any other transport (the `ext_plugin_*` C ABI,
/// a reactor, `Guest.invoke`) reporting is `unsupported` and there is no reverse caller.
extension Call {
    /// Reports a progress event to the host while the call runs. The events reach the host in the
    /// order they are reported, before the result, within the rate limit the host set; `.dropped`
    /// is routine, and the next event that passes carries the count. Safe to call from several
    /// threads.
    ///
    /// - Throws: `FrameError` with `unsupported` when the operation did not declare progress, the
    ///   host did not ask for events or the transport has no frames (stop reporting); `invalid`
    ///   for an event that breaks the rules (nothing is sent); `closed` once the call is over or
    ///   the connection ended.
    public func progress(_ event: ProgressEvent) throws -> ProgressOutcome {
        guard let reporter = channel?.reporter else { throw FrameError(.unsupported) }
        return try reporter.report(event)
    }

    /// The rate the host set for this call's events, or nil when it reports none.
    public var progressLimits: ProgressLimits? { channel?.limits }

    /// Calls an operation the host offers (declared in `Descriptor.reverse`) on behalf of this
    /// call, bounded by its deadline; nil on a transport with no frames. Several calls may be
    /// outstanding at once.
    public var reverse: ReverseCaller? { channel?.reverse }

    /// Calls a host operation on behalf of the session rather than this call, so background work
    /// may keep using it after the call returned; nil on a transport with no frames.
    public var sessionReverse: ReverseCaller? { channel?.connection.session }

    /// True once the connection to the host has ended (it closed its end or broke the rules): a
    /// handler that polls this stops early. A reverse call that is waiting is released.
    public var connectionEnded: Bool { channel?.connection.ended ?? false }
}
