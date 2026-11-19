import CExtEngine
import Foundation

/// The rules for progress events and reverse calls (docs/plugin-guest-frames.md), which are the
/// Go contract's and are pinned by lib/plugin/testdata/v1/{frame-cases,progress-limiter-cases}.json:
/// what a frame may carry, the sender's rate limit and the shape of the host's answer. The engine
/// parses and validates the requests the host sends; this judges the frames a guest writes and reads.
enum Frames {
    static let maxProgressText = 2048
    /// A retry hint is whole milliseconds, at most one year.
    static let maxRetryAfterMilliseconds: UInt64 = 365 * 24 * 60 * 60 * 1000
    static let defaultProgressEvents: Int64 = 50
    static let defaultProgressBytes: Int64 = 64 * 1024

    /// The host (or the guest's own frame) broke the framing rules.
    struct Violation: Error {}

    /// A progress event as it travels in a frame: what a guest sends, with its drop count.
    struct Event: Equatable {
        var kind: String
        var stream: String
        var text: String
        var completed: UInt64
        var total: UInt64
        var dropped: UInt64
    }

    /// A judged progress frame: the call it belongs to and the event.
    struct ProgressFrame {
        var parent: String
        var event: Event
    }

    /// A judged reverse response: the host's error, or nil for a payload.
    struct Answer {
        var error: RemoteError?
    }

    // MARK: reading JSON values

    static func strictObject(_ value: JSONValue?, fields: Set<String>) throws -> [String: JSONValue] {
        guard let object = value?.object, Set(object.keys).isSubset(of: fields) else { throw Violation() }
        return object
    }

    /// A member Go decodes into a string: absent and null are empty.
    static func text(_ object: [String: JSONValue], _ key: String) throws -> String {
        guard let value = object[key], !value.isNull else { return "" }
        guard let text = value.string else { throw Violation() }
        return text
    }

    /// A member Go decodes into a uint64: absent and null are zero; anything but a whole number is refused.
    static func count(_ object: [String: JSONValue], _ key: String) throws -> UInt64 {
        guard let value = object[key], !value.isNull else { return 0 }
        guard case .number(let text) = value, isWholeNumber(text), let number = UInt64(text) else { throw Violation() }
        return number
    }

    /// `0|[1-9][0-9]*`
    private static func isWholeNumber(_ text: String) -> Bool {
        let bytes = Array(text.utf8)
        guard let first = bytes.first, bytes.allSatisfy({ $0 >= 0x30 && $0 <= 0x39 }) else { return false }
        return first != 0x30 || bytes.count == 1
    }

    /// `[a-z0-9][a-z0-9._/-]{0,255}`
    static func identifier(_ text: String) -> Bool {
        let bytes = Array(text.utf8)
        guard (1...256).contains(bytes.count) else { return false }
        func lowerAlnum(_ b: UInt8) -> Bool { (b >= 0x61 && b <= 0x7A) || (b >= 0x30 && b <= 0x39) }
        return lowerAlnum(bytes[0])
            && bytes.dropFirst().allSatisfy { lowerAlnum($0) || $0 == 0x2E || $0 == 0x5F || $0 == 0x2F || $0 == 0x2D }
    }

    /// `[A-Za-z0-9][A-Za-z0-9._:+/-]{0,255}`
    static func revision(_ text: String) -> Bool {
        let bytes = Array(text.utf8)
        guard (1...256).contains(bytes.count) else { return false }
        func alnum(_ b: UInt8) -> Bool { (b >= 0x61 && b <= 0x7A) || (b >= 0x41 && b <= 0x5A) || (b >= 0x30 && b <= 0x39) }
        return alnum(bytes[0])
            && bytes.dropFirst().allSatisfy {
                alnum($0) || $0 == 0x2E || $0 == 0x5F || $0 == 0x3A || $0 == 0x2B || $0 == 0x2F || $0 == 0x2D
            }
    }

    // MARK: instants

    /// An instant in RFC 3339 that is not Go's zero time, as milliseconds since the epoch.
    static func instant(_ value: JSONValue?) throws -> Int64 {
        guard let text = value?.string else { throw Violation() }
        return try instant(text)
    }

    static func instant(_ text: String) throws -> Int64 {
        // YYYY-MM-DD[Tt]hh:mm:ss[.fraction](Z|z|+hh:mm|-hh:mm)
        let b = Array(text.utf8)
        func digits(_ from: Int, _ count: Int) -> Int? {
            guard from + count <= b.count else { return nil }
            var value = 0
            for byte in b[from..<from + count] {
                guard byte >= 0x30 && byte <= 0x39 else { return nil }
                value = value * 10 + Int(byte - 0x30)
            }
            return value
        }
        guard b.count >= 20, let year = digits(0, 4), b[4] == 0x2D, let month = digits(5, 2), b[7] == 0x2D,
            let day = digits(8, 2), b[10] == 0x54 || b[10] == 0x74, let hour = digits(11, 2), b[13] == 0x3A,
            let minute = digits(14, 2), b[16] == 0x3A, let second = digits(17, 2)
        else { throw Violation() }
        var at = 19
        var fraction = ""
        if at < b.count, b[at] == 0x2E {
            let start = at + 1
            at = start
            while at < b.count, b[at] >= 0x30 && b[at] <= 0x39 { at += 1 }
            guard at > start else { throw Violation() }
            fraction = String(decoding: b[start..<at], as: UTF8.self)
        }
        var offset = 0
        if at < b.count, b[at] == 0x5A || b[at] == 0x7A {
            at += 1
        } else {
            guard at + 6 == b.count, b[at] == 0x2B || b[at] == 0x2D, let hours = digits(at + 1, 2), b[at + 3] == 0x3A,
                let minutes = digits(at + 4, 2), hours <= 23, minutes <= 59
            else { throw Violation() }
            offset = (hours * 60 + minutes) * 60 * (b[at] == 0x2D ? -1 : 1)
            at += 6
        }
        guard at == b.count, (1...12).contains(month), day >= 1, day <= daysInMonth(year, month), hour <= 23,
            minute <= 59, second <= 59
        else { throw Violation() }
        let seconds = daysFromCivil(year, month, day) * 86400 + Int64(hour * 3600 + minute * 60 + second) - Int64(offset)
        let allZero = fraction.utf8.allSatisfy { $0 == 0x30 }
        // Go's zero time is the instant 0001-01-01T00:00:00Z, in whatever zone it is written.
        if seconds == daysFromCivil(1, 1, 1) * 86400 && allZero { throw Violation() }
        let millis = Int64(String((fraction + "000").prefix(3)))!
        return seconds * 1000 + millis
    }

    private static func daysInMonth(_ year: Int, _ month: Int) -> Int {
        switch month {
        case 2: return (year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)) ? 29 : 28
        case 4, 6, 9, 11: return 30
        default: return 31
        }
    }

    /// Days from 1970-01-01 to a proleptic Gregorian date.
    private static func daysFromCivil(_ year: Int, _ month: Int, _ day: Int) -> Int64 {
        let y = Int64(month <= 2 ? year - 1 : year)
        let era = (y >= 0 ? y : y - 399) / 400
        let yoe = y - era * 400
        let doy = Int64((153 * (month + (month > 2 ? -3 : 9)) + 2) / 5 + day - 1)
        let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy
        return era * 146097 + doe - 719468
    }

    /// RFC 3339 in UTC, with milliseconds only when there are some (as Java's Instant prints).
    static func format(milliseconds: Int64) -> String {
        let floorDiv = { (a: Int64, b: Int64) -> Int64 in a >= 0 ? a / b : -((-a + b - 1) / b) }
        let seconds = floorDiv(milliseconds, 1000)
        let millis = Int(milliseconds - seconds * 1000)
        let days = floorDiv(seconds, 86400)
        let inDay = Int(seconds - days * 86400)
        // civil from days (Howard Hinnant)
        let z = days + 719468
        let era = (z >= 0 ? z : z - 146096) / 146097
        let doe = z - era * 146097
        let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365
        let doy = doe - (365 * yoe + yoe / 4 - yoe / 100)
        let mp = (5 * doy + 2) / 153
        let day = Int(doy - (153 * mp + 2) / 5 + 1)
        let month = Int(mp < 10 ? mp + 3 : mp - 9)
        let year = Int(yoe + era * 400 + (month <= 2 ? 1 : 0))
        var text = String(format: "%04d-%02d-%02dT%02d:%02d:%02d", year, month, day, inDay / 3600, inDay % 3600 / 60, inDay % 60)
        if millis != 0 { text += String(format: ".%03d", millis) }
        return text + "Z"
    }

    // MARK: frames

    private static let progressFields: Set<String> = [
        "frame", "apiVersion", "parent", "kind", "stream", "text", "completed", "total", "dropped",
    ]

    /// Judges an event on its own.
    static func validateEvent(_ event: Event) throws {
        let bytes = event.text.utf8.count
        if !(event.kind == "progress" || event.kind == "log")
            || !(event.stream.isEmpty || event.stream == "out" || event.stream == "err")
            || (!event.stream.isEmpty && event.kind != "log") || bytes > maxProgressText
            || (event.total != 0 && event.completed > event.total)
        {
            throw Violation()
        }
    }

    /// Judges a progress frame: its parent (the call it belongs to) and the event.
    static func progressFrame(_ value: JSONValue) throws -> ProgressFrame {
        let object = try strictObject(value, fields: progressFields)
        let parent = try text(object, "parent")
        let event = Event(
            kind: try text(object, "kind"), stream: try text(object, "stream"), text: try text(object, "text"),
            completed: try count(object, "completed"), total: try count(object, "total"), dropped: try count(object, "dropped"))
        guard try text(object, "frame") == "progress", try text(object, "apiVersion") == apiVersion, revision(parent) else {
            throw Violation()
        }
        try validateEvent(event)
        return ProgressFrame(parent: parent, event: event)
    }

    private static let reverseRequestFields: Set<String> = [
        "frame", "apiVersion", "id", "parent", "contract", "operation", "deadline", "payload",
    ]

    /// Judges a reverse request frame, as the host reads it.
    static func reverseRequest(_ value: JSONValue) throws {
        let object = try strictObject(value, fields: reverseRequestFields)
        let ref = try strictObject(object["contract"], fields: ["name", "version"])
        let parent = try text(object, "parent")
        guard try text(object, "frame") == "reverse_request", try text(object, "apiVersion") == apiVersion,
            revision(try text(object, "id")), parent.isEmpty || revision(parent), identifier(try text(ref, "name")),
            revision(try text(ref, "version")), identifier(try text(object, "operation"))
        else { throw Violation() }
        _ = try instant(object["deadline"])
    }

    private static let reverseResponseFields: Set<String> = ["frame", "apiVersion", "id", "payload", "error"]

    /// Judges a reverse response, which a guest accepts only for the request `id` it is waiting on.
    static func reverseResponse(_ value: JSONValue, id: String) throws -> Answer {
        let object = try strictObject(value, fields: reverseResponseFields)
        guard try text(object, "frame") == "reverse_response", try text(object, "apiVersion") == apiVersion,
            try text(object, "id") == id
        else { throw Violation() }
        let failed = object["error"].map { !$0.isNull } ?? false
        // A response carries a payload (null counts) or an error, never both or neither.
        if failed == (object["payload"] != nil) { throw Violation() }
        if !failed { return Answer(error: nil) }
        let error = try strictObject(object["error"], fields: ["code", "message", "retryAfterMilliseconds"])
        let retry = try count(error, "retryAfterMilliseconds")
        let message = try text(error, "message")
        guard identifier(try text(error, "code")), message.utf8.count <= 4096, retry <= maxRetryAfterMilliseconds else {
            throw Violation()
        }
        return Answer(error: RemoteError(code: try text(error, "code"), message: message,
                                          retryAfterMilliseconds: retry == 0 ? nil : Int64(retry)))
    }

    /// Parses a frame the way the engine reads one: strictly, then into values.
    static func decode(_ frame: [UInt8]) throws -> JSONValue {
        let status = frame.withUnsafeBufferPointer { ext_host_validate_json($0.baseAddress, $0.count) }
        guard status == ext_status(EXT_OK) else { throw Violation() }
        do { return try JSONValue.parse(frame) } catch { throw Violation() }
    }

    // MARK: the sender's rate limit

    /// A token bucket counted in events and bytes of text, with a burst of twice the rate, and a
    /// count of events dropped since the last one that passed. Time is a monotonic clock in
    /// nanoseconds, given by the caller. Not thread safe.
    struct Limiter {
        private let eventRate: Double
        private let byteRate: Double
        private var events: Double
        private var bytes: Double
        private var last: UInt64
        private var dropped: UInt64 = 0

        init(eventsPerSecond: Int64, bytesPerSecond: Int64, now: UInt64) {
            eventRate = Double(eventsPerSecond > 0 ? eventsPerSecond : defaultProgressEvents)
            byteRate = Double(bytesPerSecond > 0 ? bytesPerSecond : defaultProgressBytes)
            events = 2 * eventRate
            bytes = 2 * byteRate
            last = now
        }

        /// The number of events dropped since the previous one that passed (which the caller
        /// attaches to this event), or nil when the rate limit drops it.
        mutating func admit(textBytes: Int, now: UInt64) -> UInt64? {
            if now > last {
                let delta = now - last
                // Seconds as Go's Duration.Seconds computes them.
                let elapsed = Double(delta / 1_000_000_000) + Double(delta % 1_000_000_000) / 1e9
                events = min(events + elapsed * eventRate, 2 * eventRate)
                bytes = min(bytes + elapsed * byteRate, 2 * byteRate)
                last = now
            }
            if events < 1 || bytes < Double(textBytes) {
                dropped += 1
                return nil
            }
            events -= 1
            bytes -= Double(textBytes)
            let reported = dropped
            dropped = 0
            return reported
        }

        /// The drops not yet reported, reset to zero.
        mutating func pending() -> UInt64 {
            let reported = dropped
            dropped = 0
            return reported
        }
    }

    /// The limits a request sets for a call: the rate with its defaults filled in.
    static func resolve(_ rate: [String: JSONValue]?) -> ProgressLimits {
        var events = defaultProgressEvents
        var bytes = defaultProgressBytes
        if let rate {
            events = positive(rate["eventsPerSecond"], events)
            bytes = positive(rate["bytesPerSecond"], bytes)
        }
        return ProgressLimits(eventsPerSecond: events, bytesPerSecond: bytes)
    }

    private static func positive(_ value: JSONValue?, _ fallback: Int64) -> Int64 {
        if case .number(let text)? = value, let number = Int64(text) { return number > 0 ? number : fallback }
        return fallback
    }
}
