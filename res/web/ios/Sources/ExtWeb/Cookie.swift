import Foundation

/// One browser cookie in the portable wire form (`res/web/contract`, wire version 2).
///
/// A cookie value is a credential. `description`, `debugDescription` and `dump` print the
/// cookie's identity (name, domain, path) and never its value; JSON encoding keeps the real
/// value because the protocol needs it, so treat that output as secret.
public struct Cookie: Equatable, Sendable, Codable {
    public var id: Int64
    public var ref: String
    public var name: String
    public var value: String
    public var domain: String
    public var path: String
    /// Unix seconds. 0 is a session cookie, 1 is already expired.
    public var expiry: Int64
    public var secure: Bool
    public var httpOnly: Bool
    /// `""`, `unspecified`, `none`, `lax` or `strict`.
    public var sameSitePolicy: String
    public var partitionKey: String
    public var hasCrossSiteAncestor: Bool
    /// Adapter-owned fields with namespaced keys (they contain a dot).
    public var attributes: [String: String]

    public init(
        id: Int64 = 0, ref: String = "", name: String, value: String, domain: String, path: String,
        expiry: Int64 = 0, secure: Bool = false, httpOnly: Bool = false, sameSitePolicy: String = "",
        partitionKey: String = "", hasCrossSiteAncestor: Bool = false, attributes: [String: String] = [:]
    ) {
        self.id = id
        self.ref = ref
        self.name = name
        self.value = value
        self.domain = domain
        self.path = path
        self.expiry = expiry
        self.secure = secure
        self.httpOnly = httpOnly
        self.sameSitePolicy = sameSitePolicy
        self.partitionKey = partitionKey
        self.hasCrossSiteAncestor = hasCrossSiteAncestor
        self.attributes = attributes
    }

    private enum CodingKeys: String, CodingKey {
        case id, ref, name, value, domain, path, expiry, secure
        case httpOnly = "http_only"
        case sameSitePolicy = "same_site_policy"
        case partitionKey = "partition_key"
        case hasCrossSiteAncestor = "has_cross_site_ancestor"
        case attributes
    }

    /// A missing or null member is the zero value, as in Go's decoder.
    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(Int64.self, forKey: .id) ?? 0
        ref = try container.decodeIfPresent(String.self, forKey: .ref) ?? ""
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        value = try container.decodeIfPresent(String.self, forKey: .value) ?? ""
        domain = try container.decodeIfPresent(String.self, forKey: .domain) ?? ""
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
        expiry = try container.decodeIfPresent(Int64.self, forKey: .expiry) ?? 0
        secure = try container.decodeIfPresent(Bool.self, forKey: .secure) ?? false
        httpOnly = try container.decodeIfPresent(Bool.self, forKey: .httpOnly) ?? false
        sameSitePolicy = try container.decodeIfPresent(String.self, forKey: .sameSitePolicy) ?? ""
        partitionKey = try container.decodeIfPresent(String.self, forKey: .partitionKey) ?? ""
        hasCrossSiteAncestor = try container.decodeIfPresent(Bool.self, forKey: .hasCrossSiteAncestor) ?? false
        attributes = try container.decodeIfPresent([String: String].self, forKey: .attributes) ?? [:]
    }

    /// Writes the members Go writes: `id`, `ref`, `same_site_policy`, `partition_key`,
    /// `has_cross_site_ancestor` and `attributes` only when set.
    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        if id != 0 { try container.encode(id, forKey: .id) }
        if !ref.isEmpty { try container.encode(ref, forKey: .ref) }
        try container.encode(name, forKey: .name)
        try container.encode(value, forKey: .value)
        try container.encode(domain, forKey: .domain)
        try container.encode(path, forKey: .path)
        try container.encode(expiry, forKey: .expiry)
        try container.encode(secure, forKey: .secure)
        try container.encode(httpOnly, forKey: .httpOnly)
        if !sameSitePolicy.isEmpty { try container.encode(sameSitePolicy, forKey: .sameSitePolicy) }
        if !partitionKey.isEmpty { try container.encode(partitionKey, forKey: .partitionKey) }
        if hasCrossSiteAncestor { try container.encode(true, forKey: .hasCrossSiteAncestor) }
        if !attributes.isEmpty { try container.encode(attributes, forKey: .attributes) }
    }
}

extension Cookie: CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    /// The cookie without its value. The wording is not part of the contract.
    public var description: String {
        var text = "cookie{name=\(name.debugDescription) domain=\(domain.debugDescription) path=\(path.debugDescription)"
        if !value.isEmpty { text += " value=<redacted>" }
        if !partitionKey.isEmpty { text += " partition=\(partitionKey.debugDescription)" }
        return text + "}"
    }

    public var debugDescription: String { description }

    public var customMirror: Mirror {
        Mirror(self, children: [("name", name), ("domain", domain), ("path", path)], displayStyle: .struct)
    }
}

/// A cookie scoped to one site, as one adapter hands it to another.
public struct CookieBundle: Equatable, Sendable, Codable {
    public var version: Int
    public var source: String
    public var site: String
    public var cookie: Cookie

    public init(version: Int = cookieWireVersion, source: String, site: String, cookie: Cookie) {
        self.version = version
        self.source = source
        self.site = site
        self.cookie = cookie
    }

    private enum CodingKeys: String, CodingKey { case version, source, site, cookie }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        version = try container.decodeIfPresent(Int.self, forKey: .version) ?? 0
        source = try container.decodeIfPresent(String.self, forKey: .source) ?? ""
        site = try container.decodeIfPresent(String.self, forKey: .site) ?? ""
        cookie = try container.decodeIfPresent(Cookie.self, forKey: .cookie) ?? Cookie(name: "", value: "", domain: "", path: "")
    }
}

extension CookieBundle: CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public var description: String {
        "cookie bundle{version=\(version) source=\(source.debugDescription) site=\(site.debugDescription) \(cookie)}"
    }

    public var debugDescription: String { description }

    public var customMirror: Mirror {
        Mirror(self, children: KeyValuePairs<String, Any>(dictionaryLiteral: ("version", version), ("source", source), ("site", site), ("cookie", cookie)), displayStyle: .struct)
    }
}

/// The browser share request and bundle wire version.
public let cookieWireVersion = 2

/// A cookie or bundle that breaks the portable rules. The message never contains a cookie value.
public struct CookieError: Error, Equatable, CustomStringConvertible {
    public let message: String
    public var description: String { message }
}

public enum CookieRules {
    /// `contract.CookieActive`: a session cookie, or one that expires after `now`.
    public static func isActive(_ cookie: Cookie, now: Date = Date()) -> Bool {
        cookie.expiry == 0 || cookie.expiry > Int64(now.timeIntervalSince1970.rounded(.down))
    }

    /// `contract.CookieDomainMatches`.
    public static func domainMatches(siteHost: String, cookieDomain: String) -> Bool {
        var host = GoText.lowercased(siteHost)
        if GoText.hasSuffix(host, ".") { host.unicodeScalars.removeLast() }
        let domain = GoText.lowercased(cookieDomain)
        if GoText.hasPrefix(domain, ".") {
            let base = String(domain.unicodeScalars.dropFirst())
            return host == base || GoText.hasSuffix(host, "." + base)
        }
        return host == domain
    }

    /// `contract.CookiePathMatches`: a prefix matches only at a path boundary.
    public static func pathMatches(requestPath: String, cookiePath: String) -> Bool {
        let request = Array((requestPath.isEmpty ? "/" : requestPath).utf8)
        let cookie = Array(cookiePath.utf8)
        if cookie.isEmpty || !request.starts(with: cookie) { return false }
        return request == cookie || cookie.last == UInt8(ascii: "/") || request[cookie.count] == UInt8(ascii: "/")
    }

    /// `contract.CookieMatchesSiteOptions`.
    public static func matches(_ site: Site?, _ cookie: Cookie, includeExpired: Bool = false, now: Date = Date()) -> Bool {
        guard let site else { return includeExpired || isActive(cookie, now: now) }
        return domainMatches(siteHost: site.hostname, cookieDomain: cookie.domain)
            && (site.path.isEmpty || pathMatches(requestPath: site.escapedPath, cookiePath: cookie.path))
            && (!cookie.secure || site.scheme == "https")
            && (includeExpired || isActive(cookie, now: now))
    }

    /// `contract.SameListedCookie`: the fields that identify a cookie in a listing.
    public static func sameListed(_ a: Cookie, _ b: Cookie) -> Bool {
        a.id == b.id && a.ref == b.ref && a.name == b.name && a.domain == b.domain && a.path == b.path
            && a.partitionKey == b.partitionKey && a.hasCrossSiteAncestor == b.hasCrossSiteAncestor
            && a.attributes == b.attributes
    }

    /// `contract.ValidateCookie`: the portable field rules, without requiring an unexpired
    /// cookie or a particular site.
    public static func validate(_ cookie: Cookie) throws {
        if cookie.name.isEmpty || GoText.containsAny(cookie.name, of: "()<>@,;:\\\"/[]?={} \t") || GoText.hasControl(cookie.name) {
            throw CookieError(message: "cookie has an invalid name")
        }
        let host = GoText.hasPrefix(cookie.domain, ".") ? String(cookie.domain.unicodeScalars.dropFirst()) : cookie.domain
        if host.isEmpty || GoText.containsAny(host, of: " /\\?#@\t\r\n") || GoText.hasControl(host)
            || (GoText.contains(host, scalar: ":") && !IPLiteral.isIP(host)) || GoText.hasPrefix(host, ".")
        {
            throw CookieError(message: "cookie has an invalid domain")
        }
        if !GoText.hasPrefix(cookie.path, "/") || GoText.hasControl(cookie.path) || GoText.hasControl(cookie.value) {
            throw CookieError(message: "cookie has an invalid path or value")
        }
        if (GoText.hasPrefix(cookie.name, "__Secure-") && !cookie.secure)
            || (GoText.hasPrefix(cookie.name, "__Host-")
                && (!cookie.secure || GoText.hasPrefix(cookie.domain, ".") || cookie.path != "/"))
            || (cookie.sameSitePolicy == "none" && !cookie.secure)
        {
            throw CookieError(message: "cookie violates secure prefix or SameSite requirements")
        }
        // This upper bound also keeps native microsecond expiry conversions safe.
        if cookie.expiry > 253_402_300_799 {
            throw CookieError(message: "cookie expiry exceeds the portable range")
        }
        if GoText.hasControl(cookie.partitionKey) || (cookie.hasCrossSiteAncestor && cookie.partitionKey.isEmpty) {
            throw CookieError(message: "cookie has an invalid partition scope")
        }
        if cookie.attributes.count > 32 {
            throw CookieError(message: "cookie has too many adapter attributes")
        }
        for (key, value) in cookie.attributes {
            if !GoText.contains(key, scalar: ".") || key.utf8.count > 128 || value.utf8.count > 1024
                || GoText.containsAny(key, of: " \t\r\n")
            {
                throw CookieError(message: "cookie has an invalid adapter attribute")
            }
        }
    }

    /// `contract.ValidateCookieBundle`: the site parses, the cookie validates, is active and
    /// matches the site, and SameSite is one the contract knows.
    public static func validate(_ bundle: CookieBundle, now: Date = Date()) throws {
        if bundle.version != cookieWireVersion {
            throw CookieError(message: "unsupported browser cookie bundle version")
        }
        let site: Site
        do {
            site = try parseSite(bundle.site)
        } catch let error as SiteError {
            throw CookieError(message: error.message)
        }
        try validate(bundle.cookie)
        if !matches(site, bundle.cookie, now: now) {
            throw CookieError(message: "cookie bundle has an invalid or expired site scope")
        }
        switch bundle.cookie.sameSitePolicy {
        case "", "unspecified", "none", "lax", "strict":
            break
        default:
            throw CookieError(message: "cookie bundle has an unsupported SameSite policy")
        }
    }
}
