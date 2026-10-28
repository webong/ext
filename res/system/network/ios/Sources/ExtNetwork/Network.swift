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

/// Observes interfaces and assigned addresses only. It performs no lookup and
/// creates no network connection.
public final class SystemNetworkObserver: @unchecked Sendable {
    private let options: NetworkOptions

    public init(options: NetworkOptions = try! NetworkOptions()) { self.options = options }

    public func observe() -> NetworkSnapshot {
        let now = networkTimestamp(Date())
        var first: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&first) == 0, let first else {
            return NetworkSnapshot(platform: networkPlatform, observedAt: now, interfaces: [], routes: [], resolver: NetworkResolver(),
                interfaceStatus: NetworkCollectionStatus(state: "unavailable", detail: "network interface enumeration failed", observedAt: now),
                routeStatus: unsupportedRoutes(now), resolverStatus: unsupportedResolver(now))
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
        let routes = platformRoutes(now, limit: options.maxRoutes)
        let routePartial = routes.1
        let resolver = platformResolver(now)
        return NetworkSnapshot(platform: networkPlatform, observedAt: now, interfaces: interfaces, routes: routes.0, resolver: resolver.0,
            interfaceStatus: NetworkCollectionStatus(state: partial ? "partial" : "complete",
                detail: partial ? "interface or address limit reached" : nil, observedAt: now),
            routeStatus: NetworkCollectionStatus(state: routePartial ? "partial" : "complete",
                detail: routePartial ? "route limit reached" : nil, observedAt: now),
            resolverStatus: NetworkCollectionStatus(state: resolver.1 ? "partial" : "complete",
                detail: resolver.1 ? "resolver limit reached" : nil, observedAt: now))
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

private func platformRoutes(_ now: String, limit: Int) -> ([NetworkRoute], Bool) {
    guard let output = runCommand("netstat", arguments: ["-rn"]) else {
        return ([], true)
    }
    var routes: [NetworkRoute] = []
    var partial = false
    var section = ""
    for line in output.split(separator: "\n", omittingEmptySubsequences: true) {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed == "Internet:" { section = "inet"; continue }
        if trimmed == "Internet6:" { section = "inet6"; continue }
        if trimmed.hasPrefix("Routing tables") || trimmed.isEmpty { continue }
        let fields = trimmed.split(separator: " ", omittingEmptySubsequences: true)
        if fields.count < 4 { continue }
        if routes.count >= limit { partial = true; continue }
        let destination = String(fields[0])
        let gateway = String(fields[1])
        let netif = fields.count > 3 ? String(fields[3]) : ""
        guard let cidr = destinationToCIDR(destination, section: section) else { continue }
        let isDefault = cidr == "0.0.0.0/0" || cidr == "::/0"
        routes.append(NetworkRoute(destination: cidr, gateway: routeGateway(gateway) ?? "", interface: netif,
            metric: 0, default: isDefault))
    }
    return (routes, partial)
}

// destinationToCIDR converts a netstat destination to CIDR. Network
// destinations without an explicit prefix are skipped rather than guessed.
private func destinationToCIDR(_ destination: String, section: String) -> String? {
    if destination == "default" {
        return section == "inet6" ? "::/0" : "0.0.0.0/0"
    }
    if destination.contains("/") { return destination }
    if let ip = IPAddress(destination) {
        return ip.isIPv4 ? "\(destination)/32" : "\(destination)/128"
    }
    return nil
}

// routeGateway returns the gateway address, or nil for on-link routes.
private func routeGateway(_ gateway: String) -> String? {
    if gateway.isEmpty || gateway == "*" || gateway == "default" { return nil }
    if IPAddress(gateway) != nil { return gateway }
    return nil // link-layer or link#N: on-link
}

private func platformResolver(_ now: String) -> (NetworkResolver, Bool) {
    guard let output = runCommand("scutil", arguments: ["--dns"]) else {
        return (NetworkResolver(), true)
    }
    var servers: [String] = []
    var search: [String] = []
    for line in output.split(separator: "\n", omittingEmptySubsequences: true) {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        guard trimmed.hasPrefix("nameserver[") || trimmed.hasPrefix("domain") else { continue }
        guard let colon = trimmed.firstIndex(of: ":") else { continue }
        let key = String(trimmed[trimmed.startIndex..<colon]).trimmingCharacters(in: .whitespaces)
        let value = String(trimmed[trimmed.index(after: colon)...]).trimmingCharacters(in: .whitespaces)
        if key.hasPrefix("nameserver") {
            if let ip = IPAddress(value), !servers.contains(ip.string) {
                servers.append(ip.string)
            }
        } else if key == "domain" && !search.contains(value) {
            search.append(value)
        }
    }
    return (NetworkResolver(servers: servers, search: search, options: []), servers.isEmpty && search.isEmpty)
}

private func runCommand(_ executable: String, arguments: [String]) -> String? {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/usr/bin/\(executable)")
    process.arguments = arguments
    let pipe = Pipe()
    process.standardOutput = pipe
    process.standardError = nil
    do { try process.run() } catch { return nil }
    process.waitUntilExit()
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    return String(data: data, encoding: .utf8)
}

private struct IPAddress {
    let string: String
    let isIPv4: Bool
    init?(_ value: String) {
        // IPv4 dotted-decimal.
        let parts = value.split(separator: ".", omittingEmptySubsequences: true)
        if parts.count == 4, parts.allSatisfy({ Int($0) != nil && (0...255).contains(Int($0)!) }) {
            self.string = value; self.isIPv4 = true; return
        }
        // IPv6 (very permissive: contains colons and is not a MAC address).
        if value.contains(":") && !value.contains("-") && !value.hasPrefix("link#") {
            self.string = value; self.isIPv4 = false; return
        }
        return nil
    }
}

private func unsupportedRoutes(_ now: String) -> NetworkCollectionStatus {
    NetworkCollectionStatus(state: "unsupported", detail: "native route-table observation is unavailable through this Apple binding", observedAt: now)
}

private func unsupportedResolver(_ now: String) -> NetworkCollectionStatus {
    NetworkCollectionStatus(state: "unsupported", detail: "native resolver configuration is unavailable through this Apple binding", observedAt: now)
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
