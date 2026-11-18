import Foundation

/// A parsed JSON value that keeps a number as the text it was written in, so a count can be
/// told from a fraction and a payload is never re-encoded. The engine validates frames
/// strictly (`ext_host_validate_json`: depth, UTF-8, duplicate keys, trailing data) before
/// this reads them, so the parser here is small.
indirect enum JSONValue: Equatable {
    case null
    case bool(Bool)
    case number(String)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    var object: [String: JSONValue]? {
        if case .object(let members) = self { return members }
        return nil
    }

    var string: String? {
        if case .string(let text) = self { return text }
        return nil
    }

    var isNull: Bool {
        if case .null = self { return true }
        return false
    }

    static func parse(_ bytes: [UInt8]) throws -> JSONValue {
        var parser = Parser(bytes: bytes)
        parser.skipSpace()
        let value = try parser.value()
        parser.skipSpace()
        guard parser.at == bytes.count else { throw JSONError.invalid }
        return value
    }

    enum JSONError: Error { case invalid }

    private struct Parser {
        let bytes: [UInt8]
        var at = 0

        init(bytes: [UInt8]) { self.bytes = bytes }

        mutating func skipSpace() {
            while at < bytes.count, bytes[at] == 0x20 || bytes[at] == 0x09 || bytes[at] == 0x0A || bytes[at] == 0x0D { at += 1 }
        }

        mutating func value() throws -> JSONValue {
            guard at < bytes.count else { throw JSONError.invalid }
            switch bytes[at] {
            case UInt8(ascii: "{"): return try object()
            case UInt8(ascii: "["): return try array()
            case UInt8(ascii: "\""): return .string(try string())
            case UInt8(ascii: "t"): try literal("true"); return .bool(true)
            case UInt8(ascii: "f"): try literal("false"); return .bool(false)
            case UInt8(ascii: "n"): try literal("null"); return .null
            default: return try number()
            }
        }

        mutating func literal(_ word: String) throws {
            let expected = Array(word.utf8)
            guard bytes.count - at >= expected.count, Array(bytes[at..<at + expected.count]) == expected else {
                throw JSONError.invalid
            }
            at += expected.count
        }

        mutating func number() throws -> JSONValue {
            let start = at
            while at < bytes.count, "+-0123456789.eE".utf8.contains(bytes[at]) { at += 1 }
            guard at > start else { throw JSONError.invalid }
            return .number(String(decoding: bytes[start..<at], as: UTF8.self))
        }

        mutating func string() throws -> String {
            at += 1
            var out: [UInt8] = []
            while true {
                guard at < bytes.count else { throw JSONError.invalid }
                let byte = bytes[at]
                at += 1
                if byte == UInt8(ascii: "\"") { return String(decoding: out, as: UTF8.self) }
                if byte < 0x20 { throw JSONError.invalid }
                if byte != UInt8(ascii: "\\") {
                    out.append(byte)
                    continue
                }
                guard at < bytes.count else { throw JSONError.invalid }
                let escape = bytes[at]
                at += 1
                switch escape {
                case UInt8(ascii: "\""): out.append(0x22)
                case UInt8(ascii: "\\"): out.append(0x5C)
                case UInt8(ascii: "/"): out.append(0x2F)
                case UInt8(ascii: "b"): out.append(0x08)
                case UInt8(ascii: "f"): out.append(0x0C)
                case UInt8(ascii: "n"): out.append(0x0A)
                case UInt8(ascii: "r"): out.append(0x0D)
                case UInt8(ascii: "t"): out.append(0x09)
                case UInt8(ascii: "u"):
                    var scalar = try hex4()
                    if (0xD800...0xDBFF).contains(scalar) {
                        // A pair, or a lone surrogate, which Go's decoder reads as U+FFFD.
                        if at + 1 < bytes.count, bytes[at] == 0x5C, bytes[at + 1] == UInt8(ascii: "u") {
                            let save = at
                            at += 2
                            let low = try hex4()
                            if (0xDC00...0xDFFF).contains(low) {
                                scalar = 0x10000 + ((scalar - 0xD800) << 10) + (low - 0xDC00)
                            } else {
                                at = save
                                scalar = 0xFFFD
                            }
                        } else {
                            scalar = 0xFFFD
                        }
                    } else if (0xDC00...0xDFFF).contains(scalar) {
                        scalar = 0xFFFD
                    }
                    out += Array(String(Unicode.Scalar(scalar) ?? "\u{FFFD}").utf8)
                default: throw JSONError.invalid
                }
            }
        }

        mutating func hex4() throws -> UInt32 {
            guard at + 4 <= bytes.count else { throw JSONError.invalid }
            var value: UInt32 = 0
            for index in 0..<4 {
                let byte = bytes[at + index]
                let digit: UInt32
                switch byte {
                case 0x30...0x39: digit = UInt32(byte - 0x30)
                case 0x61...0x66: digit = UInt32(byte - 0x61 + 10)
                case 0x41...0x46: digit = UInt32(byte - 0x41 + 10)
                default: throw JSONError.invalid
                }
                value = (value << 4) | digit
            }
            at += 4
            return value
        }

        mutating func object() throws -> JSONValue {
            at += 1
            var members: [String: JSONValue] = [:]
            skipSpace()
            if at < bytes.count, bytes[at] == UInt8(ascii: "}") {
                at += 1
                return .object(members)
            }
            while true {
                skipSpace()
                guard at < bytes.count, bytes[at] == UInt8(ascii: "\"") else { throw JSONError.invalid }
                let key = try string()
                skipSpace()
                guard at < bytes.count, bytes[at] == UInt8(ascii: ":") else { throw JSONError.invalid }
                at += 1
                skipSpace()
                members[key] = try value()
                skipSpace()
                guard at < bytes.count else { throw JSONError.invalid }
                let next = bytes[at]
                at += 1
                if next == UInt8(ascii: "}") { return .object(members) }
                guard next == UInt8(ascii: ",") else { throw JSONError.invalid }
            }
        }

        mutating func array() throws -> JSONValue {
            at += 1
            var items: [JSONValue] = []
            skipSpace()
            if at < bytes.count, bytes[at] == UInt8(ascii: "]") {
                at += 1
                return .array(items)
            }
            while true {
                skipSpace()
                items.append(try value())
                skipSpace()
                guard at < bytes.count else { throw JSONError.invalid }
                let next = bytes[at]
                at += 1
                if next == UInt8(ascii: "]") { return .array(items) }
                guard next == UInt8(ascii: ",") else { throw JSONError.invalid }
            }
        }
    }

    /// A JSON string literal: quotes, backslashes and control characters escaped.
    static func quote(_ text: String) -> String {
        var out = "\""
        for scalar in text.unicodeScalars {
            switch scalar {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\n": out += "\\n"
            case "\r": out += "\\r"
            case "\t": out += "\\t"
            default:
                if scalar.value < 0x20 {
                    out += String(format: "\\u%04x", scalar.value)
                } else {
                    out.unicodeScalars.append(scalar)
                }
            }
        }
        return out + "\""
    }
}
