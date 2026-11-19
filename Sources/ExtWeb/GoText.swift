import Foundation

/// Text rules that follow Go's strings, unicode and utf8 packages.
///
/// The ext.web fixtures are defined by Go's behavior. Swift compares Strings by
/// grapheme cluster and canonical equivalence ("\r\n" is one Character, and a
/// combining mark attaches to the space before it), so the web rules work on UTF-8
/// bytes and Unicode scalars instead of Characters.
enum GoText {
    /// unicode.IsSpace.
    static func isSpace(_ scalar: Unicode.Scalar) -> Bool {
        switch scalar.value {
        case 0x09...0x0D, 0x20, 0x85, 0xA0, 0x1680, 0x2000...0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
            return true
        default:
            return false
        }
    }

    /// strings.TrimSpace.
    static func trimSpace(_ text: String) -> String {
        let scalars = Array(text.unicodeScalars)
        var start = 0
        var end = scalars.count
        while start < end, isSpace(scalars[start]) { start += 1 }
        while end > start, isSpace(scalars[end - 1]) { end -= 1 }
        var view = String.UnicodeScalarView()
        view.append(contentsOf: scalars[start..<end])
        return String(view)
    }

    /// strings.TrimSpace(text) == "".
    static func isBlank(_ text: String) -> Bool {
        text.unicodeScalars.allSatisfy(isSpace)
    }

    static func hasPrefix(_ text: String, _ prefix: String) -> Bool {
        text.utf8.starts(with: prefix.utf8)
    }

    static func hasSuffix(_ text: String, _ suffix: String) -> Bool {
        text.utf8.reversed().starts(with: suffix.utf8.reversed())
    }

    /// strings.CutPrefix: the rest after `prefix`, or nil when `text` does not start with it.
    static func cutPrefix(_ text: String, _ prefix: String) -> String? {
        guard hasPrefix(text, prefix) else { return nil }
        return String(decoding: Array(text.utf8.dropFirst(prefix.utf8.count)), as: UTF8.self)
    }

    /// strings.Split on one ASCII byte.
    static func split(_ text: String, on separator: UInt8) -> [String] {
        var parts: [String] = []
        var current: [UInt8] = []
        for byte in text.utf8 {
            if byte == separator {
                parts.append(String(decoding: current, as: UTF8.self))
                current.removeAll(keepingCapacity: true)
            } else {
                current.append(byte)
            }
        }
        parts.append(String(decoding: current, as: UTF8.self))
        return parts
    }

    /// Any rune below U+0020 or U+007F.
    static func hasControl(_ text: String) -> Bool {
        text.unicodeScalars.contains { $0.value < 0x20 || $0.value == 0x7F }
    }

    /// strings.ContainsAny.
    static func containsAny(_ text: String, of characters: String) -> Bool {
        let set = Set(characters.unicodeScalars)
        return text.unicodeScalars.contains { set.contains($0) }
    }

    static func contains(_ text: String, scalar: Unicode.Scalar) -> Bool {
        text.unicodeScalars.contains(scalar)
    }

    /// unicode.ToLower, the simple one-to-one mapping. Swift's full mapping turns U+0130 into
    /// two scalars; Go's simple mapping gives "i".
    static func lowercase(_ scalar: Unicode.Scalar) -> Unicode.Scalar {
        if scalar.value < 0x80 {
            return scalar.value >= 0x41 && scalar.value <= 0x5A ? Unicode.Scalar(scalar.value + 0x20)! : scalar
        }
        if scalar.value == 0x130 { return "i" }
        let mapped = scalar.properties.lowercaseMapping.unicodeScalars
        return mapped.count == 1 ? mapped.first! : scalar
    }

    /// strings.ToLower.
    static func lowercased(_ text: String) -> String {
        var view = String.UnicodeScalarView()
        view.append(contentsOf: text.unicodeScalars.map(lowercase))
        return String(view)
    }

    /// Bytes to text the way Go's range loop and strings.ToLower see them: each byte that
    /// does not start a valid sequence becomes U+FFFD on its own. (Swift's own decoding
    /// replaces a whole truncated sequence with one U+FFFD.)
    static func decodeLossy(_ bytes: [UInt8]) -> String {
        var view = String.UnicodeScalarView()
        var index = 0
        while index < bytes.count {
            if let (scalar, length) = decodeScalar(bytes, at: index) {
                view.append(scalar)
                index += length
            } else {
                view.append("\u{FFFD}")
                index += 1
            }
        }
        return String(view)
    }

    /// The text of valid UTF-8, or nil: the check utf8.Valid makes.
    static func decodeStrict(_ bytes: [UInt8]) -> String? {
        var index = 0
        while index < bytes.count {
            guard let (_, length) = decodeScalar(bytes, at: index) else { return nil }
            index += length
        }
        return String(decoding: bytes, as: UTF8.self)
    }

    private static func decodeScalar(_ bytes: [UInt8], at index: Int) -> (Unicode.Scalar, Int)? {
        let first = bytes[index]
        func continuation(_ offset: Int, _ low: UInt8 = 0x80, _ high: UInt8 = 0xBF) -> UInt32? {
            guard index + offset < bytes.count else { return nil }
            let byte = bytes[index + offset]
            return byte >= low && byte <= high ? UInt32(byte & 0x3F) : nil
        }
        switch first {
        case 0x00...0x7F:
            return (Unicode.Scalar(first), 1)
        case 0xC2...0xDF:
            guard let b1 = continuation(1) else { return nil }
            return (Unicode.Scalar((UInt32(first & 0x1F) << 6) | b1)!, 2)
        case 0xE0...0xEF:
            let low: UInt8 = first == 0xE0 ? 0xA0 : 0x80
            let high: UInt8 = first == 0xED ? 0x9F : 0xBF
            guard let b1 = continuation(1, low, high), let b2 = continuation(2) else { return nil }
            return (Unicode.Scalar((UInt32(first & 0x0F) << 12) | (b1 << 6) | b2)!, 3)
        case 0xF0...0xF4:
            let low: UInt8 = first == 0xF0 ? 0x90 : 0x80
            let high: UInt8 = first == 0xF4 ? 0x8F : 0xBF
            guard let b1 = continuation(1, low, high), let b2 = continuation(2), let b3 = continuation(3) else {
                return nil
            }
            return (Unicode.Scalar((UInt32(first & 0x07) << 18) | (b1 << 12) | (b2 << 6) | b3)!, 4)
        default:
            return nil
        }
    }
}

/// The JSON text Go's encoding/json writes, which the userscript revision digest and the
/// replay source are made of: compact, with `<`, `>`, `&`, U+2028 and U+2029 escaped.
enum GoJSON {
    private static let hex = Array("0123456789abcdef".utf8)

    static func quote(_ text: String) -> String {
        var out: [UInt8] = [0x22]
        for scalar in text.unicodeScalars {
            switch scalar.value {
            case 0x22: out += [0x5C, 0x22]
            case 0x5C: out += [0x5C, 0x5C]
            case 0x08: out += [0x5C, UInt8(ascii: "b")]
            case 0x0C: out += [0x5C, UInt8(ascii: "f")]
            case 0x0A: out += [0x5C, UInt8(ascii: "n")]
            case 0x0D: out += [0x5C, UInt8(ascii: "r")]
            case 0x09: out += [0x5C, UInt8(ascii: "t")]
            case 0x00..<0x20, 0x3C, 0x3E, 0x26, 0x2028, 0x2029:
                let value = scalar.value
                out += [0x5C, UInt8(ascii: "u")]
                out += [hex[Int(value >> 12) & 15], hex[Int(value >> 8) & 15], hex[Int(value >> 4) & 15], hex[Int(value) & 15]]
            default:
                out += Array(String(scalar).utf8)
            }
        }
        out.append(0x22)
        return String(decoding: out, as: UTF8.self)
    }

    /// A string array; nil is `null`, which differs from `[]` (and has a different digest).
    static func array(_ items: [String]?) -> String {
        guard let items else { return "null" }
        return "[" + items.map(quote).joined(separator: ",") + "]"
    }
}
