#if os(macOS)
import Darwin
import Foundation
import SystemConfiguration

// The kernel supplies a bounded local routing snapshot. No child process or
// network connection is created. Retry a growing table at most three times.
func platformRoutes(_ now: String, limit: Int) -> ([NetworkRoute], NetworkCollectionStatus) {
    var mib: [Int32] = [CTL_NET, PF_ROUTE, 0, AF_UNSPEC, NET_RT_DUMP, 0]
    for _ in 0..<3 {
        var size = 0
        guard sysctl(&mib, UInt32(mib.count), nil, &size, nil, 0) == 0,
              size >= 0, size <= 16 * 1024 * 1024 else {
            return ([], NetworkCollectionStatus(state: "unavailable", detail: "route snapshot unavailable or exceeds byte bound", observedAt: now))
        }
        if size == 0 { return ([], NetworkCollectionStatus(state: "complete", observedAt: now)) }
        var data = Data(count: size)
        let result = data.withUnsafeMutableBytes { sysctl(&mib, UInt32(mib.count), $0.baseAddress, &size, nil, 0) }
        if result != 0 {
            if errno == ENOMEM { continue }
            break
        }
        guard size <= data.count else { break }
        data.count = size
        let (routes, partial) = parseNativeRoutes(data, limit: limit)
        return (routes, NetworkCollectionStatus(state: partial ? "partial" : "complete",
            detail: partial ? "route snapshot truncated, malformed, or limited" : nil, observedAt: now))
    }
    return ([], NetworkCollectionStatus(state: "unavailable", detail: "route snapshot changed or could not be read", observedAt: now))
}

func parseNativeRoutes(_ data: Data, limit: Int) -> ([NetworkRoute], Bool) {
    var routes: [NetworkRoute] = []
    var offset = 0
    var partial = false
    let headerSize = MemoryLayout<rt_msghdr>.size
    while offset < data.count {
        guard data.count - offset >= headerSize else { return (routes, true) }
        let header = data.withUnsafeBytes { $0.loadUnaligned(fromByteOffset: offset, as: rt_msghdr.self) }
        let length = Int(header.rtm_msglen)
        guard length >= headerSize, length <= data.count - offset else { return (routes, true) }
        let end = offset + length
        defer { offset = end }
        guard header.rtm_version == RTM_VERSION else { partial = true; continue }
        var addresses: [Int: [UInt8]] = [:]
        var cursor = offset + headerSize
        var valid = true
        for index in 0..<Int(RTAX_MAX) where header.rtm_addrs & (1 << index) != 0 {
            guard cursor + 2 <= end else { valid = false; break }
            let count = Int(data[cursor])
            // Darwin routing sockaddrs are aligned to 4 bytes, including on arm64.
            let padded = count == 0 ? 4 : (count + 3) & ~3
            guard padded <= end - cursor else { valid = false; break }
            addresses[index] = Array(data[cursor..<(cursor + count)])
            cursor += padded
        }
        guard valid else { partial = true; continue }
        guard let destination = addresses[Int(RTAX_DST)], destination.count >= 2 else { partial = true; continue }
        let family = Int32(destination[1])
        guard family == AF_INET || family == AF_INET6 else { continue }
        guard var ip = nativeAddressBytes(destination, family: family, compact: false) else { partial = true; continue }
        let mask: [UInt8]
        if let raw = addresses[Int(RTAX_NETMASK)] {
            guard let bytes = nativeAddressBytes(raw, family: family, compact: true) else { partial = true; continue }
            mask = bytes
        } else if header.rtm_flags & RTF_HOST != 0 {
            mask = Array(repeating: 255, count: ip.count)
        } else {
            partial = true; continue
        }
        var prefix = 0
        var zeroSeen = false
        var contiguous = true
        for byte in mask {
            for bit in (0..<8).reversed() {
                if byte & (UInt8(1) << bit) != 0 {
                    if zeroSeen { contiguous = false }
                    prefix += 1
                } else { zeroSeen = true }
            }
        }
        guard contiguous else { partial = true; continue }
        for index in ip.indices { ip[index] &= mask[index] }
        guard let destinationText = nativeIPText(ip, family: family) else { partial = true; continue }
        var gateway: String?
        if let raw = addresses[Int(RTAX_GATEWAY)], raw.count >= 2,
           Int32(raw[1]) == family, let bytes = nativeAddressBytes(raw, family: family, compact: false) {
            gateway = nativeIPText(bytes, family: family)
        }
        var name = [CChar](repeating: 0, count: Int(IF_NAMESIZE))
        let interface = if_indextoname(UInt32(header.rtm_index), &name) == nil ? nil : String(cString: name)
        if routes.count >= limit { partial = true; continue }
        routes.append(NetworkRoute(destination: "\(destinationText)/\(prefix)", gateway: gateway,
            interface: interface, default: prefix == 0))
    }
    return (routes, partial)
}

private func nativeAddressBytes(_ raw: [UInt8], family: Int32, compact: Bool) -> [UInt8]? {
    let start = family == AF_INET ? 4 : 8
    let count = family == AF_INET ? 4 : 16
    if !compact && raw.count < start + count { return nil }
    var bytes = [UInt8](repeating: 0, count: count)
    if raw.count > start {
        for index in 0..<min(count, raw.count - start) { bytes[index] = raw[start + index] }
    }
    // Route messages may embed the interface scope in a link-local IPv6 address.
    if !compact && family == AF_INET6 && bytes[0] == 0xfe && bytes[1] & 0xc0 == 0x80 {
        bytes[2] = 0; bytes[3] = 0
    }
    return bytes
}

private func nativeIPText(_ bytes: [UInt8], family: Int32) -> String? {
    var text = [CChar](repeating: 0, count: Int(INET6_ADDRSTRLEN))
    return bytes.withUnsafeBytes {
        inet_ntop(family, $0.baseAddress, &text, socklen_t(text.count)) == nil ? nil : String(cString: text)
    }
}

func platformResolver(_ now: String) -> (NetworkResolver, NetworkCollectionStatus) {
    guard let store = SCDynamicStoreCreate(nil, "ExtNetwork" as CFString, nil, nil),
          let values = SCDynamicStoreCopyMultiple(store, ["State:/Network/Global/DNS"] as CFArray,
              ["State:/Network/Service/[^/]+/DNS"] as CFArray) as? [String: Any] else {
        return (NetworkResolver(), NetworkCollectionStatus(state: "unavailable", detail: "SystemConfiguration DNS state unavailable", observedAt: now))
    }
    let resolver = parseNativeDNS(values)
    // Service DNS state is public; the complete synthesized/scoped resolver
    // policy is not. Do not claim that this subset describes every resolver.
    return (resolver, NetworkCollectionStatus(state: "partial",
        detail: "SystemConfiguration DNS state; supplemental resolver policy is not represented", observedAt: now))
}

func parseNativeDNS(_ values: [String: Any]) -> NetworkResolver {
    var servers = Set<String>()
    var search = Set<String>()
    for key in values.keys.sorted().prefix(256) {
        guard let dns = values[key] as? [String: Any] else { continue }
        for value in (dns["ServerAddresses"] as? [String] ?? []).prefix(256) where value.utf8.count <= 128 {
            for family in [AF_INET, AF_INET6] {
                var bytes = [UInt8](repeating: 0, count: family == AF_INET ? 4 : 16)
                if inet_pton(family, value, &bytes) == 1, let text = nativeIPText(bytes, family: family) { servers.insert(text) }
            }
        }
        // DomainName is a resolver match domain, not necessarily a search suffix.
        for value in (dns["SearchDomains"] as? [String] ?? []).prefix(256)
            where !value.isEmpty && value.utf8.count <= 253 && !value.contains("\n") {
            search.insert(value)
        }
    }
    return NetworkResolver(servers: servers.sorted(), search: search.sorted())
}
#endif
