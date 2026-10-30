import Darwin
import Foundation

public struct NetworkCollectionStatus: Codable, Equatable, Sendable {
    public let state: String
    public let detail: String?
    public let observedAt: String?

    public init(state: String, detail: String? = nil, observedAt: String? = nil) {
        self.state = state; self.detail = detail; self.observedAt = observedAt
    }

    enum CodingKeys: String, CodingKey {
        case state, detail
        case observedAt = "observed_at"
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
    NetworkCollectionStatus(state: "unsupported", detail: "route-table observation is unavailable on this Apple platform", observedAt: now)
}

private func resolverUnsupported(_ now: String) -> NetworkCollectionStatus {
    NetworkCollectionStatus(state: "unsupported", detail: "DNS resolver configuration is unavailable on this Apple platform", observedAt: now)
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
    let parsed = IPv6AddressScope(ip)
    return NetworkAddress(ip: ip, prefixLength: prefix, family: family == AF_INET ? "ipv4" : "ipv6", scope: parsed.scope)
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

private struct IPv6AddressScope {
    let scope: String
    init(_ value: String) {
        if value == "127.0.0.1" || value == "::1" { scope = "loopback" }
        else if value.hasPrefix("169.254.") || value.lowercased().hasPrefix("fe8") || value.lowercased().hasPrefix("fe9") || value.lowercased().hasPrefix("fea") || value.lowercased().hasPrefix("feb") { scope = "link" }
        else if value.hasPrefix("10.") || value.hasPrefix("192.168.") || isPrivate172(value) || value.lowercased().hasPrefix("fc") || value.lowercased().hasPrefix("fd") { scope = "private" }
        else { scope = "global" }
    }
}

private func isPrivate172(_ value: String) -> Bool {
    let parts = value.split(separator: ".", maxSplits: 2)
    guard parts.count > 1, parts[0] == "172", let second = Int(parts[1]) else { return false }
    return (16...31).contains(second)
}
