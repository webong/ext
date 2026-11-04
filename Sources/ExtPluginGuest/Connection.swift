import Foundation

// The system calls by their plain names, outside any type that has a method of the same name.
func sysWrite(_ descriptor: Int32, _ bytes: UnsafeRawPointer, _ count: Int) -> Int { write(descriptor, bytes, count) }
func sysRead(_ descriptor: Int32, _ bytes: UnsafeMutableRawPointer, _ count: Int) -> Int { read(descriptor, bytes, count) }

/// What the descriptor declares about progress and reverse calls, read from the descriptor the
/// engine validated and serves, so a call and the handshake agree on it.
struct Declarations {
    private var progress = Set<String>()
    private var reverse = Set<String>()

    /// Declares nothing.
    init() {}

    init(descriptorJSON: [UInt8]) throws {
        let descriptor = try JSONValue.parse(descriptorJSON).object ?? [:]
        read(descriptor["contracts"], progressOnly: true)
        read(descriptor["reverse"], progressOnly: false)
    }

    private mutating func read(_ contracts: JSONValue?, progressOnly: Bool) {
        guard case .array(let list)? = contracts else { return }
        for item in list {
            guard let contract = item.object, case .array(let operations)? = contract["operations"] else { continue }
            for entry in operations {
                guard let operation = entry.object else { continue }
                if progressOnly, operation["progress"] != .bool(true) { continue }
                let key = Declarations.key(contract["name"]?.string ?? "", contract["version"]?.string ?? "", operation["name"]?.string ?? "")
                if progressOnly { progress.insert(key) } else { reverse.insert(key) }
            }
        }
    }

    private static func key(_ contract: String, _ version: String, _ operation: String) -> String {
        contract + "\u{0}" + version + "\u{0}" + operation
    }

    /// The operation reports progress.
    func progress(_ contract: ContractRef, _ operation: String) -> Bool {
        progress.contains(Declarations.key(contract.name, contract.version, operation))
    }

    /// The plugin may call this host operation.
    func reverse(_ contract: ContractRef, _ operation: String) -> Bool {
        reverse.contains(Declarations.key(contract.name, contract.version, operation))
    }
}

/// The session ended badly: the host broke the framing rules, or the pipe failed.
public enum JSONLineError: Error, Equatable, CustomStringConvertible {
    /// The host sent a frame it may not (a second request during a call, an unknown frame, an
    /// answer to a request never made, a malformed answer).
    case protocolViolation
    case io(String)

    public var description: String {
        switch self {
        case .protocolViolation: return "plugin protocol violation"
        case .io(let reason): return reason
        }
    }
}

/// One JSON-line connection with its host, which a guest reads on a thread of its own so a handler
/// can wait for the host's answer to a reverse call while the connection is still read
/// (docs/plugin-guest-frames.md). The reader sends a request to the call loop (`next()`), an answer
/// to the reverse call waiting for it, and anything else to a violation that ends the session.
/// Every frame the guest writes goes through one lock.
final class Connection: @unchecked Sendable {
    static let maxFrame = 24 * 1024 * 1024
    static let sessionReverseMilliseconds: Int64 = 30_000

    /// One reverse call waiting for its answer. Guarded by `condition`.
    private final class Slot {
        var done = false
        var payload: [UInt8]?
        var error: RemoteError?
        var closed = false
    }

    let declared: Declarations
    private let output: Int32
    private let writeLock = NSLock()
    private let condition = NSCondition()
    private var waiting: [String: Slot] = [:]
    private var requests: [[UInt8]] = []
    private var issued: Int64 = 0
    private var inFlight = 0  // requests queued or being answered
    private var handshaking = true  // the hello is not answered yet
    private var isEnded = false
    private var isViolated = false
    private var reason: JSONLineError?
    private(set) var session: ReverseCaller!

    init(declared: Declarations, output: Int32) {
        self.declared = declared
        self.output = output
        self.session = Reverse(connection: self, parent: "", deadline: nil, channel: nil)
    }

    /// Reads frames until the connection ends, on a thread of its own, so a blocked read never holds the call loop.
    func start(input: Int32) {
        let thread = Thread { [self] in read(input) }
        thread.name = "ext-plugin-reader"
        thread.start()
    }

    private func read(_ descriptor: Int32) {
        let reader = FrameReader(descriptor: descriptor)
        do {
            while let frame = try reader.next() {
                try route(frame)
            }
            end(nil, violation: false)
        } catch let error as JSONLineError {
            end(error, violation: false)
        } catch {
            end(.protocolViolation, violation: true)
        }
    }

    private func route(_ frame: [UInt8]) throws {
        if rawMember(in: frame, key: "frame") == nil {
            // A request, or something the engine will refuse. The host sends none during a call;
            // the one that may follow the hello right away is not a second call.
            condition.lock()
            defer { condition.unlock() }
            if inFlight >= (handshaking ? 2 : 1) { throw Frames.Violation() }
            inFlight += 1
            requests.append(frame)
            condition.broadcast()
            return
        }
        let decoded = try Frames.decode(frame)
        guard let envelope = decoded.object, envelope["frame"]?.string == "reverse_response" else { throw Frames.Violation() }
        let id = envelope["id"]?.string ?? ""
        condition.lock()
        let waiter = waiting[id]
        let issuedNow = issued
        condition.unlock()
        if let waiter {
            // Judged against the request it answers.
            let answer = try Frames.reverseResponse(decoded, id: id)
            let payload = answer.error == nil ? rawMember(in: frame, key: "payload") : nil
            condition.lock()
            waiting[id] = nil
            waiter.payload = payload
            waiter.error = answer.error
            waiter.done = true
            condition.broadcast()
            condition.unlock()
            return
        }
        // An answer to a request that was abandoned is dropped; one to an id never issued is a violation.
        let bytes = Array(id.utf8)
        guard bytes.count >= 2, bytes[0] == UInt8(ascii: "g"), bytes.dropFirst().allSatisfy({ $0 >= 0x30 && $0 <= 0x39 }),
            let number = Int64(String(id.dropFirst())), number >= 1, number <= issuedNow
        else { throw Frames.Violation() }
    }

    /// The next request, in the order the host sent them, or nil once the connection has ended and none is left.
    func next() -> [UInt8]? {
        condition.lock()
        defer { condition.unlock() }
        while true {
            if isViolated { return nil }
            if !requests.isEmpty { return requests.removeFirst() }
            if isEnded { return nil }
            condition.wait()
        }
    }

    /// The request taken last is answered: the next one may come.
    func idle() {
        condition.lock()
        inFlight -= 1
        handshaking = false
        condition.unlock()
    }

    /// Ends the connection, releasing every reverse call still waiting. The first reason stands.
    func end(_ error: JSONLineError?, violation: Bool) {
        condition.lock()
        defer { condition.unlock() }
        if isEnded { return }
        isEnded = true
        isViolated = violation
        reason = error
        for slot in waiting.values {
            slot.closed = true
            slot.done = true
        }
        waiting.removeAll()
        condition.broadcast()
    }

    var ended: Bool {
        condition.lock()
        defer { condition.unlock() }
        return isEnded
    }

    /// The host broke the framing rules.
    var violated: Bool {
        condition.lock()
        defer { condition.unlock() }
        return isViolated
    }

    /// Why the session ended badly (a violation, or a read that failed), or nil.
    var failure: JSONLineError? {
        condition.lock()
        defer { condition.unlock() }
        return reason
    }

    /// Writes one frame; frames never interleave.
    func write(_ frame: [UInt8]) throws {
        writeLock.lock()
        defer { writeLock.unlock() }
        var bytes = frame
        bytes.append(0x0A)
        var offset = 0
        while offset < bytes.count {
            let written = bytes.withUnsafeBytes { raw in
                sysWrite(output, raw.baseAddress! + offset, raw.count - offset)
            }
            if written < 0 {
                if errno == EINTR { continue }
                throw JSONLineError.io("write: " + String(cString: strerror(errno)))
            }
            offset += written
        }
    }

    /// A caller for the call whose request id is `parent`, bound by that call's deadline.
    func forCall(parent: String, deadline: Int64, channel: CallChannel) -> ReverseCaller {
        Reverse(connection: self, parent: parent, deadline: deadline, channel: channel)
    }

    static func now() -> Int64 { Int64((Date().timeIntervalSince1970 * 1000).rounded(.down)) }

    /// Calls a host operation and waits for the answer. `parent` is the id of the call it
    /// belongs to, empty for the session; `deadline` is when to give up (milliseconds since the epoch).
    func reverse(parent: String, contract: ContractRef, operation: String, payload: Data?, deadline: Int64) throws -> Data {
        guard declared.reverse(contract, operation) else {
            throw RemoteError(code: "reverse_undeclared", message: "reverse call refused")
        }
        let slot = Slot()
        condition.lock()
        issued += 1
        let id = "g\(issued)"
        condition.unlock()
        let frame = try Connection.reverseRequest(
            id: id, parent: parent, contract: contract, operation: operation, payload: payload, deadline: deadline)
        condition.lock()
        if isEnded {
            condition.unlock()
            throw FrameError(.closed)
        }
        waiting[id] = slot
        condition.unlock()
        do {
            try write(frame)
        } catch {
            abandon(id)
            throw FrameError(.closed)
        }
        let limit = Date(timeIntervalSince1970: Double(deadline) / 1000)
        condition.lock()
        while !slot.done {
            if !condition.wait(until: limit) && !slot.done {
                waiting[id] = nil
                condition.unlock()
                throw FrameError(.deadline)
            }
        }
        let closed = slot.closed
        let error = slot.error
        let answer = slot.payload
        condition.unlock()
        if closed { throw FrameError(.closed) }
        if let error { throw error }
        return Data(answer ?? Array("null".utf8))
    }

    private func abandon(_ id: String) {
        condition.lock()
        waiting[id] = nil
        condition.unlock()
    }

    private static func reverseRequest(
        id: String, parent: String, contract: ContractRef, operation: String, payload: Data?, deadline: Int64
    ) throws -> [UInt8] {
        var head = "{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\",\"id\":" + JSONValue.quote(id)
        if !parent.isEmpty { head += ",\"parent\":" + JSONValue.quote(parent) }
        head += ",\"contract\":{\"name\":" + JSONValue.quote(contract.name) + ",\"version\":" + JSONValue.quote(contract.version)
        head += "},\"operation\":" + JSONValue.quote(operation) + ",\"deadline\":" + JSONValue.quote(Frames.format(milliseconds: deadline))
        var frame = Array(head.utf8)
        if let payload {
            frame += Array(",\"payload\":".utf8)
            frame += Array(payload)
        }
        frame.append(UInt8(ascii: "}"))
        do {
            if frame.count > maxFrame { throw Frames.Violation() }
            try Frames.reverseRequest(try Frames.decode(frame))
        } catch {
            throw FrameError(.invalid)
        }
        return frame
    }

    /// A reverse caller: for one call (bounded by its deadline), or for the session (30 seconds a call).
    private final class Reverse: ReverseCaller, @unchecked Sendable {
        let connection: Connection
        let parent: String
        let deadline: Int64?
        let channel: CallChannel?

        init(connection: Connection, parent: String, deadline: Int64?, channel: CallChannel?) {
            self.connection = connection
            self.parent = parent
            self.deadline = deadline
            self.channel = channel
        }

        func call(_ contract: ContractRef, _ operation: String, payload: Data?) throws -> Data {
            try connection.reverse(
                parent: parent, contract: contract, operation: operation, payload: payload,
                deadline: try bound(channel == nil ? Connection.sessionReverseMilliseconds : Int64.max))
        }

        func call(_ contract: ContractRef, _ operation: String, payload: Data?, timeout: TimeInterval) throws -> Data {
            let wait = Int64(max(0, min(timeout, Double(Int64.max / 4000))) * 1000)
            return try connection.reverse(
                parent: parent, contract: contract, operation: operation, payload: payload, deadline: try bound(wait))
        }

        // When to give up: after the wait asked for, and for a call never past its deadline.
        private func bound(_ wait: Int64) throws -> Int64 {
            let now = Connection.now()
            guard let channel, let deadline else { return now + wait }
            // A request names the call it belongs to, so it cannot outlive it.
            if channel.isOver { throw FrameError(.closed) }
            return wait >= deadline - now ? deadline : now + wait
        }
    }
}

/// Reads the newline-delimited frames of a file descriptor.
final class FrameReader {
    private let descriptor: Int32
    private var buffer = [UInt8](repeating: 0, count: 64 * 1024)
    private var start = 0
    private var end = 0

    init(descriptor: Int32) { self.descriptor = descriptor }

    /// The next frame without its newline, or nil at a clean end of input.
    func next() throws -> [UInt8]? {
        var line: [UInt8] = []
        while true {
            if start == end {
                let count = buffer.withUnsafeMutableBytes { sysRead(descriptor, $0.baseAddress!, $0.count) }
                if count < 0 {
                    if errno == EINTR { continue }
                    throw JSONLineError.io("read: " + String(cString: strerror(errno)))
                }
                if count == 0 {
                    if line.isEmpty { return nil }
                    throw JSONLineError.io("truncated frame")
                }
                start = 0
                end = count
            }
            if let newline = buffer[start..<end].firstIndex(of: 0x0A) {
                line += buffer[start..<newline]
                start = newline + 1
                if line.count > Connection.maxFrame { throw JSONLineError.io("frame too large") }
                return line
            }
            line += buffer[start..<end]
            start = end
            if line.count > Connection.maxFrame { throw JSONLineError.io("frame too large") }
        }
    }
}

/// The progress events and reverse callers of one call, on a connection that carries frames.
final class CallChannel: @unchecked Sendable {
    let connection: Connection
    let reporter: Reporter?
    let limits: ProgressLimits?
    private let lock = NSLock()
    private var over = false
    private(set) var reverse: ReverseCaller!

    init(connection: Connection, id: String, deadline: Int64, limits: ProgressLimits?) {
        self.connection = connection
        self.limits = limits
        self.reporter = limits.map { Reporter(connection: connection, parent: id, limits: $0) }
        self.reverse = connection.forCall(parent: id, deadline: deadline, channel: self)
    }

    var isOver: Bool {
        lock.lock()
        defer { lock.unlock() }
        return over
    }

    /// The handler returned: closes progress with a marker for events never reported.
    func finish() {
        lock.lock()
        over = true
        lock.unlock()
        reporter?.finish()
    }
}

/// Sends the progress events of one call, within the rate limit the host set.
final class Reporter: @unchecked Sendable {
    private let connection: Connection
    private let parent: String
    private let lock = NSLock()
    private var limiter: Frames.Limiter
    private var finished = false

    init(connection: Connection, parent: String, limits: ProgressLimits) {
        self.connection = connection
        self.parent = parent
        self.limiter = Frames.Limiter(
            eventsPerSecond: limits.eventsPerSecond, bytesPerSecond: limits.bytesPerSecond, now: DispatchTime.now().uptimeNanoseconds)
    }

    /// Events reach the host in the order they are reported, and none after `finish()`.
    func report(_ event: ProgressEvent) throws -> ProgressOutcome {
        let candidate = Frames.Event(
            kind: event.kind, stream: event.stream, text: event.text, completed: event.completed, total: event.total, dropped: 0)
        do { try Frames.validateEvent(candidate) } catch { throw FrameError(.invalid) }
        lock.lock()
        defer { lock.unlock() }
        guard let dropped = limiter.admit(textBytes: candidate.text.utf8.count, now: DispatchTime.now().uptimeNanoseconds) else {
            return .dropped
        }
        if finished { throw FrameError(.closed) }
        var sent = candidate
        sent.dropped = dropped
        try write(sent)
        return .sent
    }

    /// Ends reporting after a marker for events never reported: nothing may follow the response.
    func finish() {
        lock.lock()
        defer { lock.unlock() }
        if finished { return }
        finished = true
        let pending = limiter.pending()
        if pending > 0 {
            // The connection may be gone; there is nobody to tell.
            try? write(Frames.Event(kind: "log", stream: "", text: "", completed: 0, total: 0, dropped: pending))
        }
    }

    private func write(_ event: Frames.Event) throws {
        var frame = "{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\",\"parent\":" + JSONValue.quote(parent)
        frame += ",\"kind\":" + JSONValue.quote(event.kind)
        if !event.stream.isEmpty { frame += ",\"stream\":" + JSONValue.quote(event.stream) }
        if !event.text.isEmpty { frame += ",\"text\":" + JSONValue.quote(event.text) }
        if event.completed != 0 { frame += ",\"completed\":\(event.completed)" }
        if event.total != 0 { frame += ",\"total\":\(event.total)" }
        if event.dropped != 0 { frame += ",\"dropped\":\(event.dropped)" }
        do { try connection.write(Array((frame + "}").utf8)) } catch { throw FrameError(.closed) }
    }
}
