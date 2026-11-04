import Foundation
import zlib

/// Classic ZIP with bounded decompression and central-directory file-type validation.
enum ExtensionZIP {
    static func decode(_ data: Data) throws -> [ExtensionArtifacts.Item] {
        let b = [UInt8](data)
        func u(_ at: Int, _ size: Int) throws -> Int {
            guard at >= 0, at <= b.count - size else { throw ExtensionArtifacts.Failure() }
            return (0..<size).reduce(0) { $0 | Int(b[at + $1]) << (8 * $1) }
        }
        func name(_ at: Int, _ size: Int) throws -> String {
            guard at >= 0, at <= b.count - size, let value = String(bytes: b[at..<at+size], encoding: .utf8) else { throw ExtensionArtifacts.Failure() }
            return value
        }
        guard b.count >= 22 else { throw ExtensionArtifacts.Failure() }
        var end = -1
        for i in stride(from: b.count - 22, through: max(0, b.count - 65557), by: -1) {
            if try u(i, 4) == 0x06054b50, try i + 22 + u(i + 20, 2) == b.count { end = i; break }
        }
        guard end >= 0, try u(end + 4, 2) == 0, try u(end + 6, 2) == 0, try u(end + 8, 2) == u(end + 10, 2) else { throw ExtensionArtifacts.Failure() }
        let count = try u(end + 10, 2), central = try u(end + 16, 4), centralSize = try u(end + 12, 4)
        guard count <= 4096, central + centralSize == end else { throw ExtensionArtifacts.Failure() }
        var at = central, total = 0, result: [ExtensionArtifacts.Item] = []
        for _ in 0..<count {
            guard try u(at, 4) == 0x02014b50 else { throw ExtensionArtifacts.Failure() }
            let flags = try u(at + 8, 2), method = try u(at + 10, 2), length = try u(at + 28, 2)
            let checksum = try u(at + 16, 4), packed = try u(at + 20, 4), size = try u(at + 24, 4)
            let offset = try u(at + 42, 4), attrs = try u(at + 38, 4)
            let next = try at + 46 + length + u(at + 30, 2) + u(at + 32, 2)
            guard next <= end, try u(at + 34, 2) == 0, flags & 0x41 == 0,
                packed <= ExtensionArtifacts.maxZIP, size <= ExtensionArtifacts.maxFile else { throw ExtensionArtifacts.Failure() }
            let filename = try name(at + 46, length)
            try ExtensionArtifacts.safeName(filename.hasSuffix("/") ? String(filename.dropLast()) : filename)
            let kind = (attrs >> 16) & 0o170000, host = try u(at + 5, 1)
            guard ![3, 19].contains(host) || [0, 0o100000, 0o040000].contains(kind) else { throw ExtensionArtifacts.Failure() }
            at = next
            if filename.hasSuffix("/") || attrs & 16 != 0 || kind == 0o040000 { continue }
            total += size
            guard result.count < ExtensionArtifacts.maxFiles, total <= ExtensionArtifacts.maxTotal, offset <= central - 30,
                try u(offset, 4) == 0x04034b50, try u(offset + 6, 2) == flags, try u(offset + 8, 2) == method else { throw ExtensionArtifacts.Failure() }
            let localLength = try u(offset + 26, 2), dataAt = try offset + 30 + localLength + u(offset + 28, 2)
            guard dataAt + packed <= central, try name(offset + 30, localLength).utf8.elementsEqual(filename.utf8) else { throw ExtensionArtifacts.Failure() }
            if flags & 8 == 0 {
                guard try u(offset + 14, 4) == checksum, try u(offset + 18, 4) == packed, try u(offset + 22, 4) == size else { throw ExtensionArtifacts.Failure() }
            }
            let content: Data
            if method == 0 {
                guard size == packed else { throw ExtensionArtifacts.Failure() }
                content = Data(b[dataAt..<dataAt+packed])
            } else if method == 8 {
                var stream = z_stream(), buffer = [UInt8](repeating: 0, count: size + 1)
                guard inflateInit2_(&stream, -15, ZLIB_VERSION, Int32(MemoryLayout<z_stream>.size)) == Z_OK else { throw ExtensionArtifacts.Failure() }
                defer { inflateEnd(&stream) }
                let status = b.withUnsafeBytes { input in
                    buffer.withUnsafeMutableBytes { output in
                        stream.next_in = UnsafeMutablePointer(mutating: input.bindMemory(to: Bytef.self).baseAddress!.advanced(by: dataAt))
                        stream.avail_in = UInt32(packed)
                        stream.next_out = output.bindMemory(to: Bytef.self).baseAddress!
                        stream.avail_out = UInt32(size + 1)
                        return inflate(&stream, Z_FINISH)
                    }
                }
                guard status == Z_STREAM_END, stream.total_in == packed, stream.total_out == size else { throw ExtensionArtifacts.Failure() }
                content = Data(buffer.prefix(size))
            } else { throw ExtensionArtifacts.Failure() }
            guard crc(content) == checksum else { throw ExtensionArtifacts.Failure() }
            result.append(.init(name: filename, data: content))
        }
        guard at == end else { throw ExtensionArtifacts.Failure() }; return result
    }
    private static func crc(_ data: Data) -> Int {
        data.withUnsafeBytes { Int(crc32(0, $0.bindMemory(to: Bytef.self).baseAddress, UInt32(data.count))) }
    }
    static func encode(_ files: [ExtensionArtifacts.Item]) throws -> Data {
        var out = Data(), central = Data()
        func put(_ data: inout Data, _ number: Int, _ size: Int) {
            for i in 0..<size { data.append(UInt8(truncatingIfNeeded: number >> (8 * i))) }
        }
        for file in files {
            let name = Data(file.name.utf8), checksum = crc(file.data), offset = out.count
            guard name.count <= 65535, out.count + central.count + 76 + name.count * 2 + file.data.count + 22 <= ExtensionArtifacts.maxZIP else { throw ExtensionArtifacts.Failure() }
            for (n, s) in [(0x04034b50,4),(20,2),(0x800,2),(0,2),(0,2),(33,2),(checksum,4),(file.data.count,4),(file.data.count,4),(name.count,2),(0,2)] { put(&out,n,s) }
            out.append(name); out.append(file.data)
            for (n, s) in [(0x02014b50,4),(0x314,2),(20,2),(0x800,2),(0,2),(0,2),(33,2),(checksum,4),(file.data.count,4),(file.data.count,4),(name.count,2),(0,2),(0,2),(0,2),(0,2),(0o100644 << 16,4),(offset,4)] { put(&central,n,s) }
            central.append(name)
        }
        let offset = out.count
        out.append(central)
        for (n, s) in [(0x06054b50,4),(0,2),(0,2),(files.count,2),(files.count,2),(central.count,4),(offset,4),(0,2)] { put(&out,n,s) }
        return out
    }
}
