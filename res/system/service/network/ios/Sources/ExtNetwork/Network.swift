import Darwin
import Foundation

/// A state (complete, partial, permission-denied, unsupported or unavailable) and, where something
/// is missing, why: unsupported, permission, no_device or tool_missing (`testdata/capability/v1`).
public struct NetworkCollectionStatus: Codable, Equatable, Sendable {
    public let state: String
    public let reason: String?
    public let detail: String?
    public let observedAt: String?

    public init(state: String, reason: String? = nil, detail: String? = nil, observedAt: String? = nil) {
        self.state = state; self.reason = reason; self.detail = detail; self.observedAt = observedAt
    }

    enum CodingKeys: String, CodingKey {
        case state, reason, detail
        case observedAt = "observed_at"
    }

    /// Checks that the state is known and the reason fits it: none for complete and partial,
    /// permission or none for permission-denied, unsupported or tool_missing for unsupported, and
    /// none, permission, no_device or tool_missing for unavailable (a failed native read has none).
    public func validate() throws {
        let allowed: [String?]
        switch state {
        case "complete", "partial": allowed = [nil]
        case "permission-denied": allowed = [nil, "permission"]
        case "unsupported": allowed = ["unsupported", "tool_missing"]
        case "unavailable": allowed = [nil, "permission", "no_device", "tool_missing"]
        default: throw NetworkObservationError.invalidStatus
        }
        guard allowed.contains(where: { $0 == reason }) else { throw NetworkObservationError.invalidStatus }
    }
}

public struct NetworkAddress: Codable, Equatable, Sendable {
    public let ip: String
    public let prefixLength: Int
    public let family: String
    public let scope: String

    public init(ip: String, prefixLength: Int, family: String, scope: String) {
        self.ip = ip; self.prefixLength = prefixLength; self.family = family; self.scope = scope
    }

    /// Builds an address from a literal IPv4 or IPv6 address and a prefix length: the canonical text
    /// (RFC 5952, lower case), the family and the scope (`testdata/network/v1`). It refuses a zone, a
    /// bracket, a leading zero in an IPv4 octet, a host name and a prefix beyond the family's width.
    public static func parse(_ ip: String, prefixLength: Int) throws -> NetworkAddress {
        let bytes = try addressBytes(ip)
        let v4 = bytes.count == 4
        guard (0...(v4 ? 32 : 128)).contains(prefixLength) else { throw NetworkObservationError.invalidAddress }
        return NetworkAddress(ip: formatAddress(bytes), prefixLength: prefixLength,
                              family: v4 ? "ipv4" : "ipv6", scope: addressScope(bytes))
    }

    /// The canonical text of a literal address (see `parse`).
    public static func canonical(_ ip: String) throws -> String { formatAddress(try addressBytes(ip)) }

    enum CodingKeys: String, CodingKey {
        case ip, family, scope
        case prefixLength = "prefix_length"
    }
}

public struct NetworkInterface: Codable, Equatable, Sendable {
    public let index: Int
    public let name: String
    public let mtu: Int
    public let hardwareAddress: String?
    public let flags: [String]
    public let addresses: [NetworkAddress]

    public init(index: Int, name: String, mtu: Int = 0, hardwareAddress: String? = nil,
                flags: [String], addresses: [NetworkAddress]) {
        self.index = index; self.name = name; self.mtu = mtu; self.hardwareAddress = hardwareAddress
        self.flags = flags; self.addresses = addresses
    }

    enum CodingKeys: String, CodingKey {
        case index, name, mtu, flags, addresses
        case hardwareAddress = "hardware_address"
    }
}

public struct NetworkRoute: Codable, Equatable, Sendable {
    public let destination: String
    public let gateway: String?
    public let interface: String?
    public let metric: Int
    public let `default`: Bool

    public init(destination: String, gateway: String? = nil, interface: String? = nil,
                metric: Int = 0, default: Bool = false) {
        self.destination = destination; self.gateway = gateway; self.interface = interface
        self.metric = metric; self.default = `default`
    }
}

public struct NetworkResolver: Codable, Equatable, Sendable {
    public let servers: [String]
    public let search: [String]
    public let options: [String]

    public init(servers: [String] = [], search: [String] = [], options: [String] = []) {
        self.servers = servers; self.search = search; self.options = options
    }

    /// Trims each entry (leading and trailing U+0009 to U+000D and U+0020) and drops empty and
    /// repeated entries, keeping the first of each and the order the system reported
    /// (`testdata/network/v1`).
    public static func clean(_ values: [String]) -> [String] {
        func space(_ scalar: Unicode.Scalar) -> Bool { scalar == " " || (9...13).contains(scalar.value) }
        var seen = Set<String>()
        var out: [String] = []
        for value in values {
            let scalars = value.unicodeScalars
            guard let start = scalars.firstIndex(where: { !space($0) }), let end = scalars.lastIndex(where: { !space($0) }) else { continue }
            let trimmed = String(scalars[start...end])
            if seen.insert(trimmed).inserted { out.append(trimmed) }
        }
        return out
    }
}

public struct NetworkSnapshot: Codable, Equatable, Sendable {
    public let platform: String
    public let observedAt: String
    public let interfaces: [NetworkInterface]
    public let routes: [NetworkRoute]
    public let resolver: NetworkResolver
    public let interfaceStatus: NetworkCollectionStatus
    public let routeStatus: NetworkCollectionStatus
    public let resolverStatus: NetworkCollectionStatus

    public init(platform: String, observedAt: String, interfaces: [NetworkInterface],
                routes: [NetworkRoute], resolver: NetworkResolver,
                interfaceStatus: NetworkCollectionStatus, routeStatus: NetworkCollectionStatus,
                resolverStatus: NetworkCollectionStatus) {
        self.platform = platform; self.observedAt = observedAt; self.interfaces = interfaces
        self.routes = routes; self.resolver = resolver; self.interfaceStatus = interfaceStatus
        self.routeStatus = routeStatus; self.resolverStatus = resolverStatus
    }

    enum CodingKeys: String, CodingKey {
        case platform, interfaces, routes, resolver
        case observedAt = "observed_at"
        case interfaceStatus = "interface_status"
        case routeStatus = "route_status"
        case resolverStatus = "resolver_status"
    }
}

public struct NetworkOptions: Equatable, Sendable {
    public let maxInterfaces: Int
    public let maxAddresses: Int
    public let maxRoutes: Int

    public init(maxInterfaces: Int = 256, maxAddresses: Int = 256, maxRoutes: Int = 4096) throws {
        guard (1...65_536).contains(maxInterfaces), (1...65_536).contains(maxAddresses), (1...65_536).contains(maxRoutes) else {
            throw NetworkObservationError.invalidLimit
        }
        self.maxInterfaces = maxInterfaces; self.maxAddresses = maxAddresses; self.maxRoutes = maxRoutes
    }
}

public enum NetworkObservationError: Error, Equatable {
    case invalidLimit
    case invalidAddress
    case invalidStatus

    /// The stable error code of `res/system/testdata/codes.json`.
    public var code: String { "invalid" }
}

/// Observes local interfaces and assigned addresses, plus native route and DNS
/// state on macOS. It performs no lookup and creates no network connection.
public final class SystemNetworkObserver: @unchecked Sendable {
    private let options: NetworkOptions

    public init(options: NetworkOptions = try! NetworkOptions()) { self.options = options }

    public func observe() -> NetworkSnapshot {
        let now = networkTimestamp(Date())
        var first: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&first) == 0, let first else {
            return NetworkSnapshot(platform: networkPlatform, observedAt: now, interfaces: [], routes: [], resolver: NetworkResolver(),
                interfaceStatus: NetworkCollectionStatus(state: "unavailable", detail: "network interface enumeration failed", observedAt: now),
                routeStatus: routeUnsupported(now), resolverStatus: resolverUnsupported(now))
        }
        defer { freeifaddrs(first) }
        struct Builder { var index: Int; var flags: [String]; var addresses: [NetworkAddress] }
        var builders: [String: Builder] = [:]
        var pointer: UnsafeMutablePointer<ifaddrs>? = first
        var partial = false
        while let current = pointer {
            defer { pointer = current.pointee.ifa_next }
            let name = String(cString: current.pointee.ifa_name)
            if builders[name] == nil && builders.count >= options.maxInterfaces { partial = true; continue }
            var builder = builders[name] ?? Builder(index: Int(if_nametoindex(name)), flags: networkFlags(current.pointee.ifa_flags), addresses: [])
            guard let address = current.pointee.ifa_addr else { builders[name] = builder; continue }
            let family = Int32(address.pointee.sa_family)
            guard family == AF_INET || family == AF_INET6 else { builders[name] = builder; continue }
            if builder.addresses.count >= options.maxAddresses { partial = true; builders[name] = builder; continue }
            if let value = networkAddress(address, netmask: current.pointee.ifa_netmask) { builder.addresses.append(value) }
            builders[name] = builder
        }
        let interfaces = builders.map { name, builder in
            NetworkInterface(index: builder.index, name: name, flags: builder.flags.sorted(),
                addresses: builder.addresses.sorted { $0.ip < $1.ip })
        }.sorted { $0.index < $1.index }
        let (routes, routeStatus) = platformRoutes(now, limit: options.maxRoutes)
        let (resolver, resolverStatus) = platformResolver(now)
        return NetworkSnapshot(platform: networkPlatform, observedAt: now, interfaces: interfaces, routes: routes, resolver: resolver,
            interfaceStatus: NetworkCollectionStatus(state: partial ? "partial" : "complete",
                detail: partial ? "interface or address limit reached" : nil, observedAt: now),
            routeStatus: routeStatus, resolverStatus: resolverStatus)
    }
}

private var networkPlatform: String {
#if os(iOS)
    "ios"
#else
    "darwin"
#endif
}

private func networkTimestamp(_ date: Date) -> String {
    let formatter = ISO8601DateFormatter(); formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter.string(from: date)
}

#if !os(macOS)
private func platformRoutes(_ now: String, limit: Int) -> ([NetworkRoute], NetworkCollectionStatus) {
    ([], routeUnsupported(now))
}

private func platformResolver(_ now: String) -> (NetworkResolver, NetworkCollectionStatus) {
    (NetworkResolver(), resolverUnsupported(now))
}
#endif

private func routeUnsupported(_ now: String) -> NetworkCollectionStatus {
    NetworkCollectionStatus(state: "unsupported", reason: "unsupported", detail: "route-table observation is unavailable on this Apple platform", observedAt: now)
}

private func resolverUnsupported(_ now: String) -> NetworkCollectionStatus {
    NetworkCollectionStatus(state: "unsupported", reason: "unsupported", detail: "DNS resolver configuration is unavailable on this Apple platform", observedAt: now)
}

private func networkFlags(_ raw: UInt32) -> [String] {
    var result: [String] = []
    if raw & UInt32(IFF_UP) != 0 { result.append("up") }
    if raw & UInt32(IFF_BROADCAST) != 0 { result.append("broadcast") }
    if raw & UInt32(IFF_LOOPBACK) != 0 { result.append("loopback") }
    if raw & UInt32(IFF_POINTOPOINT) != 0 { result.append("point-to-point") }
    if raw & UInt32(IFF_MULTICAST) != 0 { result.append("multicast") }
    if raw & UInt32(IFF_RUNNING) != 0 { result.append("running") }
    return result
}

private func networkAddress(_ address: UnsafeMutablePointer<sockaddr>, netmask: UnsafeMutablePointer<sockaddr>?) -> NetworkAddress? {
    let family = Int32(address.pointee.sa_family)
    var buffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
    let length: socklen_t = family == AF_INET ? socklen_t(MemoryLayout<sockaddr_in>.size) : socklen_t(MemoryLayout<sockaddr_in6>.size)
    guard getnameinfo(address, length, &buffer, socklen_t(buffer.count), nil, 0, NI_NUMERICHOST) == 0 else { return nil }
    let ip = String(cString: buffer).split(separator: "%", maxSplits: 1).first.map(String.init) ?? ""
    let prefix = netmask.map { maskPrefix($0, family: family) } ?? 0
    return try? NetworkAddress.parse(ip, prefixLength: prefix)
}

private func maskPrefix(_ mask: UnsafeMutablePointer<sockaddr>, family: Int32) -> Int {
    let bytes: [UInt8]
    if family == AF_INET {
        let value = UnsafeRawPointer(mask).assumingMemoryBound(to: sockaddr_in.self).pointee.sin_addr
        var copy = value; bytes = withUnsafeBytes(of: &copy) { Array($0) }
    } else {
        let value = UnsafeRawPointer(mask).assumingMemoryBound(to: sockaddr_in6.self).pointee.sin6_addr
        var copy = value; bytes = withUnsafeBytes(of: &copy) { Array($0) }
    }
    return bytes.reduce(0) { $0 + $1.nonzeroBitCount }
}

// MARK: addresses

private func addressBytes(_ text: String) throws -> [UInt8] {
    let raw = Array(text.utf8)
    if raw.contains(UInt8(ascii: ":")) { return try v6Bytes(raw) }
    guard let bytes = v4Bytes(raw[...]) else { throw NetworkObservationError.invalidAddress }
    return bytes
}

private func v4Bytes(_ text: ArraySlice<UInt8>) -> [UInt8]? {
    let parts = text.split(separator: UInt8(ascii: "."), omittingEmptySubsequences: false)
    guard parts.count == 4 else { return nil }
    var out: [UInt8] = []
    for part in parts {
        guard (1...3).contains(part.count), part.allSatisfy({ $0 >= 48 && $0 <= 57 }),
              part.count == 1 || part.first! != 48 else { return nil }
        let value = part.reduce(0) { $0 * 10 + Int($1 - 48) }
        guard value <= 255 else { return nil }
        out.append(UInt8(value))
    }
    return out
}

/// The 16-bit groups of one side of a "::"; the last token of the address may be an IPv4 address.
private func v6Groups(_ text: ArraySlice<UInt8>, mayEndWithV4: Bool) -> [Int]? {
    if text.isEmpty { return [] }
    let tokens = text.split(separator: UInt8(ascii: ":"), omittingEmptySubsequences: false)
    var out: [Int] = []
    for (index, token) in tokens.enumerated() {
        if token.isEmpty { return nil }
        if token.contains(UInt8(ascii: ".")) {
            guard mayEndWithV4, index == tokens.count - 1, let v4 = v4Bytes(token) else { return nil }
            out.append(Int(v4[0]) << 8 | Int(v4[1]))
            out.append(Int(v4[2]) << 8 | Int(v4[3]))
        } else {
            guard token.count <= 4, let value = Int(String(decoding: token, as: UTF8.self), radix: 16) else { return nil }
            out.append(value)
        }
    }
    return out
}

private func v6Bytes(_ raw: [UInt8]) throws -> [UInt8] {
    let colon = UInt8(ascii: ":")
    func hexOrSeparator(_ c: UInt8) -> Bool {
        c == colon || c == 46 || (48...57).contains(c) || (97...102).contains(c) || (65...70).contains(c)
    }
    guard raw.allSatisfy(hexOrSeparator) else { throw NetworkObservationError.invalidAddress }
    var gaps: [Int] = []
    var index = 0
    while index + 1 < raw.count {
        if raw[index] == colon && raw[index + 1] == colon {
            gaps.append(index)
            index += 2
            if index < raw.count && raw[index] == colon { throw NetworkObservationError.invalidAddress } // ":::"
        } else {
            index += 1
        }
    }
    guard gaps.count <= 1 else { throw NetworkObservationError.invalidAddress }
    var groups = [Int](repeating: 0, count: 8)
    if let gap = gaps.first {
        guard let head = v6Groups(raw[..<gap], mayEndWithV4: false),
              let tail = v6Groups(raw[(gap + 2)...], mayEndWithV4: true), head.count + tail.count <= 7 else {
            throw NetworkObservationError.invalidAddress
        }
        for (i, value) in head.enumerated() { groups[i] = value }
        for (i, value) in tail.enumerated() { groups[8 - tail.count + i] = value }
    } else {
        guard let all = v6Groups(raw[...], mayEndWithV4: true), all.count == 8 else { throw NetworkObservationError.invalidAddress }
        groups = all
    }
    return groups.flatMap { [UInt8($0 >> 8), UInt8($0 & 0xff)] }
}

private func formatAddress(_ bytes: [UInt8]) -> String {
    if bytes.count == 4 { return bytes.map { String($0) }.joined(separator: ".") }
    let groups = (0..<8).map { Int(bytes[2 * $0]) << 8 | Int(bytes[2 * $0 + 1]) }
    var bestStart = -1, bestLength = 0, i = 0
    while i < 8 {
        if groups[i] != 0 { i += 1; continue }
        var j = i
        while j < 8 && groups[j] == 0 { j += 1 }
        if j - i > bestLength { bestStart = i; bestLength = j - i }
        i = j
    }
    if bestLength < 2 { bestStart = -1 }
    var out = ""
    var position = 0
    while position < 8 {
        if position == bestStart {
            out += "::"
            position += bestLength
            continue
        }
        if !out.isEmpty && !out.hasSuffix(":") { out += ":" }
        out += String(groups[position], radix: 16)
        position += 1
    }
    return out
}

/// The scope of an address: IANA's special-purpose ranges, as `testdata/network/v1` lists them.
private func addressScope(_ b: [UInt8]) -> String {
    if b.count == 4 {
        if b[0] == 127 { return "loopback" }
        if b[0] == 169 && b[1] == 254 { return "link" }
        if b[0] == 224 && b[1] == 0 && b[2] == 0 { return "link" }
        if b[0] == 10 || (b[0] == 172 && (16...31).contains(b[1])) || (b[0] == 192 && b[1] == 168) { return "private" }
        if b == [0, 0, 0, 0] { return "unspecified" }
        if (224...239).contains(b[0]) { return "multicast" }
        return "global"
    }
    let leadingZeros = b[0..<15].allSatisfy { $0 == 0 }
    if leadingZeros && b[15] == 1 { return "loopback" }
    if b[0] == 0xfe && b[1] & 0xc0 == 0x80 { return "link" }
    if b[0] == 0xff && b[1] & 0x0f == 0x02 { return "link" }
    if b[0] & 0xfe == 0xfc { return "private" }
    if leadingZeros && b[15] == 0 { return "unspecified" }
    if b[0] == 0xff { return "multicast" }
    return "global"
}
