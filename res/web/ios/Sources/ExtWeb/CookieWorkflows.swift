import Foundation
import CoreFoundation

/// Portable cookie import/export. Returned strings and data contain credentials.
/// Parsing returns cookie fields, not query provenance (`source` / `source_info`).
public enum CookieWorkflows {
    public static let maxInputBytes = 8 << 20

    public static func requirePortableScope(_ cookie: Cookie) throws {
        try CookieRules.validate(cookie)
        guard cookie.partitionKey.isEmpty, !cookie.hasCrossSiteAncestor, cookie.attributes.isEmpty else {
            throw CookieError(message: "native cookie isolation cannot be represented in this format")
        }
    }

    public static func header(_ cookies: [Cookie], site: String, now: Date = Date()) throws -> String {
        let url = try parseSite(site)
        var selected: [(Int, Cookie)] = []
        for (index, cookie) in cookies.enumerated() {
            try CookieRules.validate(cookie)
            if !CookieRules.matches(url, cookie, now: now) { continue }
            try requirePortableScope(cookie)
            try requireHeaderValue(cookie.value)
            selected.append((index, cookie))
        }
        selected.sort { a, b in
            let ac = a.1.path.utf8.count, bc = b.1.path.utf8.count
            return ac == bc ? a.0 < b.0 : ac > bc
        }
        return selected.map { $0.1.name + "=" + $0.1.value }.joined(separator: "; ")
    }

    public static func requireHeaderValue(_ value: String) throws {
        guard value.utf8.allSatisfy({ b in
            b == 0x21 || (0x23...0x2b).contains(b) || (0x2d...0x3a).contains(b)
                || (0x3c...0x5b).contains(b) || (0x5d...0x7e).contains(b)
        }) else { throw CookieError(message: "cookie value cannot be represented in an HTTP header") }
    }

    /// Netscape format cannot retain SameSite or provenance. Native isolation is rejected.
    public static func netscape(_ cookies: [Cookie]) throws -> String {
        var result = "# Netscape HTTP Cookie File\n# Exported by CTX; SameSite and source metadata are not represented.\n"
        for cookie in cookies {
            try requirePortableScope(cookie)
            guard !cookie.value.contains(where: { "\t\r\n".contains($0) }) else {
                throw CookieError(message: "cookie value cannot be represented in a Netscape jar")
            }
            result += [(cookie.httpOnly ? "#HttpOnly_" : "") + cookie.domain,
                cookie.domain.hasPrefix(".") ? "TRUE" : "FALSE", cookie.path,
                cookie.secure ? "TRUE" : "FALSE", String(cookie.expiry < 0 ? 1 : cookie.expiry),
                cookie.name, cookie.value].joined(separator: "\t") + "\n"
        }
        return result
    }

    public static func bundle(_ cookie: Cookie, site: String, source: String = "inline", now: Date = Date()) throws -> CookieBundle {
        let parsed = try parseSite(site)
        // Strip query and fragment without changing escaped path or host spelling.
        let clean = String(parsed.string.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false)[0]
            .split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)[0])
        let result = CookieBundle(source: source.isEmpty ? "inline" : source, site: clean, cookie: cookie)
        try CookieRules.validate(result, now: now)
        return result
    }

    public static func parse(_ data: Data) throws -> [Cookie] {
        guard data.count <= maxInputBytes, var text = String(data: data, encoding: .utf8) else {
            throw CookieError(message: "cookie input must be UTF-8 and at most 8 MiB")
        }
        if text.hasPrefix("\u{feff}") { text.removeFirst() }
        let trimmed = GoText.trimSpace(text)
        guard !trimmed.isEmpty else { throw CookieError(message: "cookie input is empty") }
        if !trimmed.hasPrefix("[") && !trimmed.hasPrefix("{") { return try parseNetscape(text) }
        do {
            try checkDepth(trimmed)
            let json = try JSONSerialization.jsonObject(with: Data(trimmed.utf8))
            let rows: [Any]
            if let array = json as? [Any] { rows = array }
            else if let object = json as? [String: Any] {
                guard !(object.keys.contains("cookie") && object.keys.contains("cookies")) else { throw invalid() }
                if let cookie = object["cookie"] {
                    guard let version = object["version"] as? NSNumber,
                        CFGetTypeID(version) != CFBooleanGetTypeID(), version.doubleValue == 2 else { throw invalid() }
                    if let source = object["source"], !(source is NSNull), !(source is String) { throw invalid() }
                    rows = [cookie]
                } else if let array = object["cookies"] as? [Any] { rows = array }
                else { throw invalid() }
            } else { throw invalid() }
            return try rows.map { try parseObject($0) }
        } catch { throw invalid() } // Never include input or decoder diagnostics containing credentials.
    }

    // Bound nesting before invoking a decoder; cookie schemas are shallow.
    private static func checkDepth(_ text: String) throws {
        var depth = 0, quoted = false, escaped = false
        for byte in text.utf8 {
            if quoted {
                if escaped { escaped = false }
                else if byte == 0x5c { escaped = true }
                else if byte == 0x22 { quoted = false }
            } else if byte == 0x22 { quoted = true }
            else if byte == 0x7b || byte == 0x5b { depth += 1; if depth > 128 { throw invalid() } }
            else if byte == 0x7d || byte == 0x5d { depth -= 1 }
        }
    }

    private static func invalid() -> CookieError { CookieError(message: "invalid or unsupported cookie input") }
    private static func parseObject(_ raw: Any) throws -> Cookie {
        guard var o = raw as? [String: Any] else { throw invalid() }
        let allowed = Set("id ref name value domain path expiry secure http_only same_site_policy partition_key has_cross_site_ancestor attributes source source_info hostOnly httpOnly expirationDate expires session sameSite storeId firstPartyDomain partitionKey".split(separator: " ").map(String.init))
        guard Set(o.keys).isSubset(of: allowed) else { throw invalid() }
        for key in ["name", "value", "domain", "path"] { guard o[key] is String else { throw invalid() } }
        for key in ["storeId", "firstPartyDomain"] {
            if let value = o[key], !(value is NSNull), (value as? String) != "" { throw invalid() }
        }
        if let value = o["partitionKey"], !(value is NSNull) { throw invalid() }
        for (alias, key) in [("httpOnly", "http_only"), ("sameSite", "same_site_policy")] {
            if let value = o[alias] { guard o[key] == nil else { throw invalid() }; o[key] = value }
        }
        for key in ["secure", "http_only", "has_cross_site_ancestor", "same_site_policy", "partition_key"] {
            if o[key] is NSNull { throw invalid() }
        }
        var seenExpiry = false
        for key in ["expiry", "expirationDate", "expires"] {
            if let raw = o[key] {
                guard !seenExpiry, let number = raw as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID() else { throw invalid() }
                seenExpiry = true
                var seconds = number.doubleValue
                guard seconds.isFinite, seconds >= -1, seconds <= 253402300799 else { throw invalid() }
                if key == "expires" && seconds == -1 { seconds = 0 }
                o["expiry"] = Int64(seconds > 0 && seconds < 1 ? -1 : floor(seconds))
            }
        }
        if let raw = o["hostOnly"] {
            guard let number = raw as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID(), var domain = o["domain"] as? String else { throw invalid() }
            if domain.hasPrefix(".") { domain.removeFirst() }
            o["domain"] = (number.boolValue ? "" : ".") + domain
        }
        // Decode only portable fields; provenance is deliberately not part of Cookie.
        var cookie = try JSONDecoder().decode(Cookie.self, from: JSONSerialization.data(withJSONObject: o))
        if let raw = o["session"] {
            guard let number = raw as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID(), number.boolValue == (cookie.expiry == 0) else { throw invalid() }
        }
        cookie.sameSitePolicy = cookie.sameSitePolicy.lowercased()
        if cookie.sameSitePolicy == "no_restriction" { cookie.sameSitePolicy = "none" }
        try CookieRules.validate(cookie)
        return cookie
    }

    private static func parseNetscape(_ text: String) throws -> [Cookie] {
        var result: [Cookie] = []
        for raw in text.components(separatedBy: "\n") {
            var line = raw
            if line.hasSuffix("\r") { line.removeLast() }
            let httpOnly = line.hasPrefix("#HttpOnly_")
            if line.isEmpty || (line.hasPrefix("#") && !httpOnly) { continue }
            if httpOnly { line.removeFirst(10) }
            let fields = line.components(separatedBy: "\t")
            guard fields.count == 7, ["TRUE", "FALSE"].contains(fields[1]), ["TRUE", "FALSE"].contains(fields[3]),
                let expiry = Int64(fields[4]), expiry >= 0 else { throw invalid() }
            var domain = fields[0]
            if domain.hasPrefix(".") { domain.removeFirst() }
            if fields[1] == "TRUE" { domain = "." + domain }
            let cookie = Cookie(name: fields[5], value: fields[6], domain: domain, path: fields[2],
                expiry: expiry, secure: fields[3] == "TRUE", httpOnly: httpOnly)
            try CookieRules.validate(cookie)
            result.append(cookie)
        }
        return result
    }
}
