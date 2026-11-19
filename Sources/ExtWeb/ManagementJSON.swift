import Foundation

/// Syntax-only reader preserving raw payloads and arbitrary JSON number precision.
struct ManagementJSON {
    private var bytes: [UInt8]
    private var at = 0
    private var members: [String: String] = [:]
    static func read(_ text: String, object: Bool = false) throws -> [String: String] {
        var p = Self(bytes: Array(text.utf8))
        p.space()
        if object && (p.at == p.bytes.count || p.bytes[p.at] != 123) { throw ManagementError.invalidJSON }
        try p.value(0)
        p.space()
        guard p.at == p.bytes.count else { throw ManagementError.invalidJSON }
        return p.members
    }
    static func string(_ raw: String?) throws -> String {
        guard let raw, raw != "null" else { return "" }
        do { return try JSONDecoder().decode(String.self, from: Data(raw.utf8)) }
        catch { throw ManagementError.invalidJSON }
    }
    private static func canonical(_ key: String) -> String {
        let lower = key.lowercased()
        return ["version", "kind", "action", "input", "status", "result", "protocol", "endpoint"].contains(lower) ? lower : key
    }
    private mutating func space() { while at < bytes.count && [9, 10, 13, 32].contains(bytes[at]) { at += 1 } }
    private mutating func take(_ b: UInt8) -> Bool {
        if at < bytes.count && bytes[at] == b { at += 1; return true }; return false
    }
    private mutating func need(_ b: UInt8) throws { if !take(b) { throw ManagementError.invalidJSON } }
    private var digit: Bool { at < bytes.count && bytes[at] >= 48 && bytes[at] <= 57 }
    private mutating func digits() throws { guard digit else { throw ManagementError.invalidJSON }; while digit { at += 1 } }
    private mutating func quoted() throws -> String {
        let start = at
        try need(34)
        while at < bytes.count {
            let c = bytes[at]; at += 1
            if c == 34 { return String(decoding: bytes[start..<at], as: UTF8.self) }
            if c < 32 { throw ManagementError.invalidJSON }
            if c == 92 {
                guard at < bytes.count else { throw ManagementError.invalidJSON }
                let e = bytes[at]; at += 1
                if e == 117 {
                    for _ in 0..<4 {
                        guard at < bytes.count, Array("0123456789abcdefABCDEF".utf8).contains(bytes[at]) else { throw ManagementError.invalidJSON }
                        at += 1
                    }
                } else if !Array("\"\\/bfnrt".utf8).contains(e) { throw ManagementError.invalidJSON }
            }
        }
        throw ManagementError.invalidJSON
    }
    private mutating func value(_ depth: Int) throws {
        space()
        guard at < bytes.count else { throw ManagementError.invalidJSON }
        let c = bytes[at]
        if c == 123 || c == 91 {
            guard depth < 128 else { throw ManagementError.invalidJSON }
            at += 1
            let end: UInt8 = c == 123 ? 125 : 93
            space()
            if take(end) { return }
            while true {
                space()
                var key: String?
                if c == 123 {
                    let rawKey = try quoted()
                    if depth == 0 { key = try Self.string(rawKey) }
                    space(); try need(58); space()
                }
                let start = at
                try value(depth + 1)
                if depth == 0, let key { members[Self.canonical(key)] = String(decoding: bytes[start..<at], as: UTF8.self) }
                space()
                if take(end) { return }
                try need(44)
            }
        }
        if c == 34 { _ = try quoted(); return }
        for literal in ["true", "false", "null"] {
            let b = Array(literal.utf8)
            if bytes[at...].starts(with: b) { at += b.count; return }
        }
        _ = take(45)
        if !take(48) { try digits() }
        if take(46) { try digits() }
        if take(101) || take(69) { if !take(43) { _ = take(45) }; try digits() }
    }
}
