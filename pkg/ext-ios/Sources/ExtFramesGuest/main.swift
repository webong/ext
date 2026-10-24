import ExtPluginGuest
import Foundation

// The frames conformance guest (lib/plugin/testdata/v1/frames-descriptor.json,
// docs/plugin-guest-frames.md): reports progress and calls its host. It serves standard input
// and output as a JSON-line guest process, so the shared Go suite can check it
// (scripts/plugin-crosslang.sh sets EXT_SWIFT_FRAMES_GUEST to it).

let host = ContractRef(name: "host.svc", version: "v1")

func quoted(_ text: String) -> String {
    (try? JSONEncoder().encode(text)).map { String(decoding: $0, as: UTF8.self) } ?? "\"\""
}

/// How a reverse call came out, in the shape the suite reads.
func outcome(_ caller: ReverseCaller, _ operation: String, _ payload: Data?, timeout: TimeInterval? = nil) -> String {
    do {
        let result = try timeout.map { try caller.call(host, operation, payload: payload, timeout: $0) }
            ?? caller.call(host, operation, payload: payload)
        return "{\"result\":" + String(decoding: result, as: UTF8.self) + "}"
    } catch let error as RemoteError {
        let retry = (error.retryAfterMilliseconds ?? 0) != 0 ? ",\"retry\":\(error.retryAfterMilliseconds!)" : ""
        return "{\"error\":{\"code\":" + quoted(error.code) + ",\"message\":" + quoted(error.message) + retry + "}}"
    } catch let error as FrameError {
        let other: String
        switch error.code {
        case .closed: other = "closed"
        case .deadline: other = "deadline"
        case .canceled: other = "canceled"
        default: other = "failed"
        }
        return "{\"error\":{\"other\":\"\(other)\"}}"
    } catch {
        return "{\"error\":{\"other\":\"failed\"}}"
    }
}

/// Outcomes of session-scoped calls, from background work to "collect".
final class Background: @unchecked Sendable {
    private let lock = NSLock()
    private let ready = DispatchSemaphore(value: 0)
    private var outcomes: [String] = []

    func add(_ outcome: String) {
        lock.lock()
        outcomes.append(outcome)
        lock.unlock()
        ready.signal()
    }

    func take(within seconds: Double) -> String? {
        guard ready.wait(timeout: .now() + seconds) == .success else { return nil }
        lock.lock()
        defer { lock.unlock() }
        return outcomes.removeFirst()
    }
}

let background = Background()

/// Reports an event; a refusal is not the handler's failure.
@discardableResult
func report(_ call: Call, _ event: ProgressEvent) -> ProgressOutcome? {
    try? call.progress(event)
}

func input(_ call: Call) -> [String: Any] {
    guard !call.payload.isEmpty,
        let object = try? JSONSerialization.jsonObject(with: call.payload, options: [.fragmentsAllowed]) as? [String: Any]
    else { return [:] }
    return object
}

func handle(_ call: Call) throws -> Data {
    let input = input(call)
    func json(_ text: String) -> Data { Data(text.utf8) }
    switch call.operation {
    case "stream":
        report(call, .fraction(1, of: 3))
        report(call, .out("hello"))
        report(call, .err("oops"))
        return json("{\"done\":true}")
    case "flood":
        let count = (input["count"] as? NSNumber)?.intValue ?? 300
        var dropped = 0
        for _ in 0..<count where report(call, .log("x")) == .dropped { dropped += 1 }
        return json("{\"dropped\":\(dropped)}")
    case "limits":
        let limits = call.progressLimits
        return json("{\"declared\":\(limits != nil),\"events\":\(limits?.eventsPerSecond ?? 0),\"bytes\":\(limits?.bytesPerSecond ?? 0)}")
    case "split":
        guard let text = input["text"] as? String else { throw GuestFailure.invalidInput }
        let pieces = ProgressEvent.split(text)
        for piece in pieces { report(call, .out(piece)) }
        return json("{\"pieces\":\(pieces.count)}")
    case "plain":
        var unsupported = false
        do { _ = try call.progress(.log("never sent")) } catch let error as FrameError { unsupported = error.code == .unsupported }
        return json("{\"unsupported\":\(unsupported),\"limits\":\(call.progressLimits != nil)}")
    case "call":
        guard let reverse = call.reverse else { return json("{\"error\":{\"other\":\"unsupported\"}}") }
        guard let operation = input["op"] as? String else { throw GuestFailure.invalidInput }
        return json(outcome(reverse, operation, call.payloadMember("payload")))
    case "twice":
        guard let reverse = call.reverse else { throw GuestFailure.unsupported }
        let first = outcome(reverse, "sign", Data("1".utf8))
        let second = outcome(reverse, "sign", Data("2".utf8))
        let prefix = "{\"result\":"
        guard first.hasPrefix(prefix), second.hasPrefix(prefix) else { throw GuestFailure.reverseFailed }
        return json("{\"first\":" + first.dropFirst(prefix.count).dropLast() + ",\"second\":" + second.dropFirst(prefix.count).dropLast() + "}")
    case "idle":
        guard let session = call.sessionReverse else { throw GuestFailure.unsupported }
        let thread = Thread {
            Thread.sleep(forTimeInterval: 0.020)
            background.add(outcome(session, "sign", Data("{\"n\":1}".utf8), timeout: 10))
        }
        thread.start()
        return json("{\"started\":true}")
    case "collect":
        return json(background.take(within: 5) ?? "{\"timeout\":true}")
    case "burst":
        guard let reverse = call.reverse else { throw GuestFailure.unsupported }
        let lock = NSLock()
        var counts: [String: Int] = [:]
        let group = DispatchGroup()
        for _ in 0..<6 {
            group.enter()
            Thread {
                var key = "ok"
                do {
                    _ = try reverse.call(host, "hold", payload: nil)
                } catch let error as RemoteError {
                    key = error.code
                } catch {
                    key = "other"
                }
                lock.lock()
                counts[key, default: 0] += 1
                lock.unlock()
                group.leave()
            }.start()
        }
        group.wait()
        return json("{" + counts.sorted { $0.key < $1.key }.map { quoted($0.key) + ":\($0.value)" }.joined(separator: ",") + "}")
    default:
        throw GuestFailure.unknownOperation
    }
}

/// Private failures: the caller sees only a generic operation_failed.
enum GuestFailure: Error {
    case invalidInput, unsupported, reverseFailed, unknownOperation
}

let signing = Operation(name: "sign", surface: "signing")
let descriptor = Descriptor(
    identity: Identity(id: "ctx/frames", revision: "fixture-1"),
    contracts: [
        Contract(
            name: "ext.frames", version: "v1",
            operations: [
                .reporting("stream"), .reporting("flood"), .reporting("limits"), .reporting("split"), Operation(name: "plain"),
                Operation(name: "call"), Operation(name: "twice"), Operation(name: "idle"), Operation(name: "collect"),
                Operation(name: "burst"),
            ])
    ],
    reverse: [
        Contract(
            name: "host.svc", version: "v1",
            operations: [
                signing, Operation(name: "secret"), Operation(name: "empty"), Operation(name: "fail"), Operation(name: "errok"),
                Operation(name: "errretryok"), Operation(name: "big"), Operation(name: "hold"),
            ])
    ])

do {
    let guest = try Guest(descriptor: descriptor, handler: handle)
    try JSONLine.serveStandardIO(guest)
} catch {
    FileHandle.standardError.write(Data("ext-frames-guest: \(error)\n".utf8))
    exit(1)
}
