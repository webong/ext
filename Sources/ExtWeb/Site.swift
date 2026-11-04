import Foundation

/// A site URL that does not parse as an http or https URL with a hostname and no userinfo.
public struct SiteError: Error, Equatable, CustomStringConvertible {
    public let message: String
    public var description: String { message }

    static let invalid = SiteError(message: "site must be an http or https URL with a hostname")
}

/// A parsed http or https site, as `contract.ParseSite` reads it: the URL Go's net/url
/// would build, with the host lower-cased.
public struct Site: Equatable, Sendable {
    /// `http` or `https`.
    public let scheme: String
    /// The host with its port, decoded and lower-cased.
    public let host: String
    /// The host without its port or brackets.
    public let hostname: String
    /// The decoded path; empty when the URL has none.
    public let path: String
    /// The path as it would be sent (Go's EscapedPath).
    public let escapedPath: String
    public let rawQuery: String
    /// A trailing `?` with nothing after it.
    public let forceQuery: Bool
    public let fragment: String
    public let escapedFragment: String

    /// The canonical form (Go's URL.String).
    public var string: String {
        var out = scheme + ":"
        if !host.isEmpty || !path.isEmpty { out += "//" }
        out += String(decoding: URLEscape.escape(Array(host.utf8), .host), as: UTF8.self)
        if !escapedPath.isEmpty, !GoText.hasPrefix(escapedPath, "/"), !host.isEmpty { out += "/" }
        out += escapedPath
        if forceQuery || !rawQuery.isEmpty { out += "?" + rawQuery }
        if !fragment.isEmpty { out += "#" + escapedFragment }
        return out
    }
}

/// Parses a site the way `contract.ParseSite` does: http or https only, a hostname, no
/// userinfo, and a lower-cased host. The rules are Go's net/url (Go 1.27), written out for
/// this input. A host with two or more colons outside brackets is refused, as Go does with
/// `urlstrictcolons` on; Go's default for that depends on the consuming module's `go` line.
public func parseSite(_ raw: String) throws -> Site {
    var bytes = Array(raw.utf8)
    var fragment: [UInt8] = []
    if let hash = bytes.firstIndex(of: UInt8(ascii: "#")) {
        fragment = Array(bytes[(hash + 1)...])
        bytes = Array(bytes[..<hash])
    }
    if bytes.contains(where: { $0 < 0x20 || $0 == 0x7F }) { throw SiteError.invalid }

    // getScheme: letters, then letters, digits and "+-." up to the colon.
    var colon: Int?
    for (index, byte) in bytes.enumerated() {
        let letter = (byte >= 0x41 && byte <= 0x5A) || (byte >= 0x61 && byte <= 0x7A)
        if letter { continue }
        if byte == UInt8(ascii: ":") {
            if index == 0 { throw SiteError.invalid }
            colon = index
            break
        }
        let mark = (byte >= 0x30 && byte <= 0x39) || byte == UInt8(ascii: "+") || byte == UInt8(ascii: "-") || byte == UInt8(ascii: ".")
        if mark && index > 0 { continue }
        break
    }
    guard let colon else { throw SiteError.invalid }
    let scheme = String(decoding: bytes[..<colon].map { $0 >= 0x41 && $0 <= 0x5A ? $0 + 0x20 : $0 }, as: UTF8.self)
    guard scheme == "http" || scheme == "https" else { throw SiteError.invalid }
    var rest = Array(bytes[(colon + 1)...])

    var forceQuery = false
    var rawQuery: [UInt8] = []
    if rest.last == UInt8(ascii: "?"), rest.filter({ $0 == UInt8(ascii: "?") }).count == 1 {
        forceQuery = true
        rest.removeLast()
    } else if let question = rest.firstIndex(of: UInt8(ascii: "?")) {
        rawQuery = Array(rest[(question + 1)...])
        rest = Array(rest[..<question])
    }

    // A rootless path is opaque and a path without "//" has no host: both are refused.
    guard rest.starts(with: [UInt8(ascii: "/"), UInt8(ascii: "/")]) else { throw SiteError.invalid }
    var authority = Array(rest[2...])
    var rawPath: [UInt8] = []
    if let slash = authority.firstIndex(of: UInt8(ascii: "/")) {
        rawPath = Array(authority[slash...])
        authority = Array(authority[..<slash])
    }
    if authority.contains(UInt8(ascii: "@")) { throw SiteError.invalid }
    let decodedHost = try parseHost(authority)
    if splitHostPort(decodedHost).isEmpty { throw SiteError.invalid }

    // strings.ToLower on the decoded host, which turns every invalid byte into U+FFFD.
    let host: [UInt8]
    if decodedHost.allSatisfy({ $0 < 0x80 }) {
        host = decodedHost.map { $0 >= 0x41 && $0 <= 0x5A ? $0 + 0x20 : $0 }
    } else {
        host = Array(GoText.lowercased(GoText.decodeLossy(decodedHost)).utf8)
    }

    let path = try URLEscape.unescape(rawPath, .path)
    let escapedPath: [UInt8]
    if !rawPath.isEmpty, rawPath != URLEscape.escape(path, .path), URLEscape.validEncoded(rawPath, .path),
        (try? URLEscape.unescape(rawPath, .path)) == path
    {
        escapedPath = rawPath
    } else if path == [UInt8(ascii: "*")] {
        escapedPath = path
    } else {
        escapedPath = URLEscape.escape(path, .path)
    }

    var decodedFragment: [UInt8] = []
    var escapedFragment: [UInt8] = []
    if !fragment.isEmpty {
        decodedFragment = try URLEscape.unescape(fragment, .fragment)
        if fragment != URLEscape.escape(decodedFragment, .fragment), URLEscape.validEncoded(fragment, .fragment),
            (try? URLEscape.unescape(fragment, .fragment)) == decodedFragment
        {
            escapedFragment = fragment
        } else {
            escapedFragment = URLEscape.escape(decodedFragment, .fragment)
        }
    }

    // Text the way Go's range loop sees bytes: each invalid byte is one U+FFFD.
    let text = GoText.decodeLossy
    return Site(
        scheme: scheme, host: text(host), hostname: text(splitHostPort(host)), path: text(path),
        escapedPath: String(decoding: escapedPath, as: UTF8.self), rawQuery: String(decoding: rawQuery, as: UTF8.self),
        forceQuery: forceQuery, fragment: text(decodedFragment),
        escapedFragment: String(decoding: escapedFragment, as: UTF8.self))
}

/// net/url parseHost for an http or https authority.
private func parseHost(_ host: [UInt8]) throws -> [UInt8] {
    let open = UInt8(ascii: "["), close = UInt8(ascii: "]"), colon = UInt8(ascii: ":")
    if let openIndex = host.lastIndex(of: open), openIndex > 0 { throw SiteError.invalid }
    if host.first == open {
        guard let closeIndex = host.lastIndex(of: close) else { throw SiteError.invalid }
        let colonPort = Array(host[(closeIndex + 1)...])
        guard validOptionalPort(colonPort) else { throw SiteError.invalid }
        let hostname = Array(host[1..<closeIndex])
        var unescaped: [UInt8]
        let marker: [UInt8] = Array("%25".utf8)
        if let zone = indexOf(marker, in: hostname) {
            unescaped = try URLEscape.unescape(Array(hostname[..<zone]), .host)
            unescaped += try URLEscape.unescape(Array(hostname[zone...]), .zone)
        } else {
            unescaped = try URLEscape.unescape(hostname, .host)
        }
        // Only an IPv6 address may be bracketed.
        guard let parsed = IPLiteral.parseAddr(unescaped), parsed.family == .v6 else { throw SiteError.invalid }
        return [open] + unescaped + [close] + colonPort
    }
    if let first = host.firstIndex(of: colon) {
        // The port after the first colon must be digits, so a second colon is refused too.
        guard validOptionalPort(Array(host[first...])) else { throw SiteError.invalid }
    }
    return try URLEscape.unescape(host, .host)
}

private func validOptionalPort(_ port: [UInt8]) -> Bool {
    if port.isEmpty { return true }
    guard port[0] == UInt8(ascii: ":") else { return false }
    return port.dropFirst().allSatisfy { $0 >= 0x30 && $0 <= 0x39 }
}

/// net/url splitHostPort, returning the host.
private func splitHostPort(_ hostPort: [UInt8]) -> [UInt8] {
    var host = hostPort
    if let colon = host.lastIndex(of: UInt8(ascii: ":")), validOptionalPort(Array(host[colon...])) {
        host = Array(host[..<colon])
    }
    if host.first == UInt8(ascii: "["), host.last == UInt8(ascii: "]") {
        host = Array(host.dropFirst().dropLast())
    }
    return host
}

private func indexOf(_ needle: [UInt8], in haystack: [UInt8]) -> Int? {
    guard !needle.isEmpty, haystack.count >= needle.count else { return nil }
    for start in 0...(haystack.count - needle.count) where Array(haystack[start..<start + needle.count]) == needle {
        return start
    }
    return nil
}

/// net/url's percent-encoding rules for the parts of a URL a site has.
enum URLEscape {
    enum Mode { case path, host, zone, fragment }

    static func isHex(_ byte: UInt8) -> Bool {
        (byte >= 0x30 && byte <= 0x39) || (byte >= 0x61 && byte <= 0x66) || (byte >= 0x41 && byte <= 0x46)
    }

    static func unhex(_ byte: UInt8) -> UInt8 {
        byte >= 0x61 ? byte - 0x61 + 10 : byte >= 0x41 ? byte - 0x41 + 10 : byte - 0x30
    }

    static func shouldEscape(_ byte: UInt8, _ mode: Mode) -> Bool {
        if (byte >= 0x61 && byte <= 0x7A) || (byte >= 0x41 && byte <= 0x5A) || (byte >= 0x30 && byte <= 0x39) {
            return false
        }
        if mode == .host || mode == .zone {
            switch byte {
            case UInt8(ascii: "!"), UInt8(ascii: "$"), UInt8(ascii: "&"), UInt8(ascii: "'"), UInt8(ascii: "("),
                UInt8(ascii: ")"), UInt8(ascii: "*"), UInt8(ascii: "+"), UInt8(ascii: ","), UInt8(ascii: ";"),
                UInt8(ascii: "="), UInt8(ascii: ":"), UInt8(ascii: "["), UInt8(ascii: "]"), UInt8(ascii: "<"),
                UInt8(ascii: ">"), UInt8(ascii: "\""):
                return false
            default:
                break
            }
        }
        switch byte {
        case UInt8(ascii: "-"), UInt8(ascii: "_"), UInt8(ascii: "."), UInt8(ascii: "~"):
            return false
        case UInt8(ascii: "$"), UInt8(ascii: "&"), UInt8(ascii: "+"), UInt8(ascii: ","), UInt8(ascii: "/"),
            UInt8(ascii: ":"), UInt8(ascii: ";"), UInt8(ascii: "="), UInt8(ascii: "?"), UInt8(ascii: "@"):
            switch mode {
            case .path: return byte == UInt8(ascii: "?")
            case .fragment: return false
            default: break
            }
        default:
            break
        }
        if mode == .fragment {
            switch byte {
            case UInt8(ascii: "!"), UInt8(ascii: "("), UInt8(ascii: ")"), UInt8(ascii: "*"): return false
            default: break
            }
        }
        return true
    }

    static func unescape(_ text: [UInt8], _ mode: Mode) throws -> [UInt8] {
        var index = 0
        var escapes = 0
        while index < text.count {
            let byte = text[index]
            if byte == UInt8(ascii: "%") {
                escapes += 1
                guard index + 2 < text.count, isHex(text[index + 1]), isHex(text[index + 2]) else {
                    throw SiteError.invalid
                }
                let isPercent = text[index + 1] == UInt8(ascii: "2") && text[index + 2] == UInt8(ascii: "5")
                // In a host, %-encoding is for non-ASCII bytes only (and %25 in a zone).
                if mode == .host && unhex(text[index + 1]) < 8 && !isPercent { throw SiteError.invalid }
                if mode == .zone {
                    let value = unhex(text[index + 1]) << 4 | unhex(text[index + 2])
                    if !isPercent && value != 0x20 && shouldEscape(value, .host) { throw SiteError.invalid }
                }
                index += 3
            } else {
                if (mode == .host || mode == .zone) && byte < 0x80 && shouldEscape(byte, mode) { throw SiteError.invalid }
                index += 1
            }
        }
        if escapes == 0 { return text }
        var out: [UInt8] = []
        out.reserveCapacity(text.count)
        index = 0
        while index < text.count {
            if text[index] == UInt8(ascii: "%") {
                out.append(unhex(text[index + 1]) << 4 | unhex(text[index + 2]))
                index += 3
            } else {
                out.append(text[index])
                index += 1
            }
        }
        return out
    }

    static func escape(_ text: [UInt8], _ mode: Mode) -> [UInt8] {
        let upper = Array("0123456789ABCDEF".utf8)
        var out: [UInt8] = []
        out.reserveCapacity(text.count)
        for byte in text {
            if shouldEscape(byte, mode) {
                out += [UInt8(ascii: "%"), upper[Int(byte >> 4)], upper[Int(byte & 15)]]
            } else {
                out.append(byte)
            }
        }
        return out
    }

    static func validEncoded(_ text: [UInt8], _ mode: Mode) -> Bool {
        for byte in text {
            switch byte {
            case UInt8(ascii: "!"), UInt8(ascii: "$"), UInt8(ascii: "&"), UInt8(ascii: "'"), UInt8(ascii: "("),
                UInt8(ascii: ")"), UInt8(ascii: "*"), UInt8(ascii: "+"), UInt8(ascii: ","), UInt8(ascii: ";"),
                UInt8(ascii: "="), UInt8(ascii: ":"), UInt8(ascii: "@"), UInt8(ascii: "["), UInt8(ascii: "]"),
                UInt8(ascii: "%"):
                continue
            default:
                if shouldEscape(byte, mode) { return false }
            }
        }
        return true
    }
}
