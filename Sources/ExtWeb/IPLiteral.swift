import Foundation

/// IP address literals as Go's net/netip.ParseAddr reads them: what `net/url` accepts
/// between brackets and what `net.ParseIP` accepts for a cookie domain.
enum IPLiteral {
    enum Family { case v4, v6 }

    struct Parsed {
        var family: Family
        var hasZone: Bool
    }

    /// netip.ParseAddr: nil when the text is not an address.
    static func parseAddr(_ text: [UInt8]) -> Parsed? {
        for byte in text {
            switch byte {
            case UInt8(ascii: "."): return parseIPv4(text[...]) ? Parsed(family: .v4, hasZone: false) : nil
            case UInt8(ascii: ":"): return parseIPv6(text)
            case UInt8(ascii: "%"): return nil
            default: continue
            }
        }
        return nil
    }

    /// net.ParseIP(text) != nil: an address without a zone.
    static func isIP(_ text: String) -> Bool {
        guard let parsed = parseAddr(Array(text.utf8)) else { return false }
        return !parsed.hasZone
    }

    private static func parseIPv4(_ field: ArraySlice<UInt8>) -> Bool {
        var value = 0
        var position = 0
        var digits = 0
        let start = field.startIndex
        let end = field.endIndex
        for index in field.indices {
            let byte = field[index]
            if byte >= 0x30 && byte <= 0x39 {
                if digits == 1 && value == 0 { return false }
                value = value * 10 + Int(byte - 0x30)
                digits += 1
                if value > 255 { return false }
            } else if byte == UInt8(ascii: ".") {
                if index == start || index == end - 1 || field[index - 1] == UInt8(ascii: ".") { return false }
                if position == 3 { return false }
                position += 1
                value = 0
                digits = 0
            } else {
                return false
            }
        }
        return position >= 3
    }

    private static func parseIPv6(_ input: [UInt8]) -> Parsed? {
        var text = input[...]
        var hasZone = false
        if let percent = input.firstIndex(of: UInt8(ascii: "%")) {
            if percent == input.count - 1 { return nil }
            text = input[..<percent]
            hasZone = true
        }
        var filled = 0
        var ellipsis = -1
        if text.count >= 2, text[text.startIndex] == UInt8(ascii: ":"), text[text.startIndex + 1] == UInt8(ascii: ":") {
            ellipsis = 0
            text = text.dropFirst(2)
            if text.isEmpty { return Parsed(family: .v6, hasZone: hasZone) }
        }
        while filled < 16 {
            var offset = 0
            var accumulator: UInt32 = 0
            while offset < text.count {
                let byte = text[text.startIndex + offset]
                if byte >= 0x30 && byte <= 0x39 {
                    accumulator = (accumulator << 4) + UInt32(byte - 0x30)
                } else if byte >= 0x61 && byte <= 0x66 {
                    accumulator = (accumulator << 4) + UInt32(byte - 0x61 + 10)
                } else if byte >= 0x41 && byte <= 0x46 {
                    accumulator = (accumulator << 4) + UInt32(byte - 0x41 + 10)
                } else {
                    break
                }
                if offset > 3 { return nil }
                offset += 1
            }
            if offset == 0 { return nil }
            if offset < text.count, text[text.startIndex + offset] == UInt8(ascii: ".") {
                if ellipsis < 0 && filled != 12 { return nil }
                if filled + 4 > 16 { return nil }
                guard parseIPv4(text) else { return nil }
                text = text[text.endIndex...]
                filled += 4
                break
            }
            filled += 2
            text = text.dropFirst(offset)
            if text.isEmpty { break }
            if text[text.startIndex] != UInt8(ascii: ":") { return nil }
            if text.count == 1 { return nil }
            text = text.dropFirst()
            if text[text.startIndex] == UInt8(ascii: ":") {
                if ellipsis >= 0 { return nil }
                ellipsis = filled
                text = text.dropFirst()
                if text.isEmpty { break }
            }
        }
        if !text.isEmpty { return nil }
        if filled < 16 {
            if ellipsis < 0 { return nil }
        } else if ellipsis >= 0 {
            return nil
        }
        return Parsed(family: .v6, hasZone: hasZone)
    }
}
