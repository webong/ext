import Foundation
import CryptoKit
import Darwin

/// Portable unsigned extension artifacts. Hosts own native signing/installation.
/// Source and destination trees must be private and unchanged by other writers.
public enum ExtensionArtifacts {
    public struct Description: Codable, Equatable {
        public let name: String, version: String, manifestVersion: Int, id: String
        public let permissions: [String], hostPermissions: [String]
        public let files: Int, bytes: Int, revision: String
    }
    public struct Failure: Error, CustomStringConvertible {
        public var description: String { "invalid or unsafe extension artifact" }
        public init() {}
    }
    static let maxFiles = 2048, maxFile = 16 << 20, maxTotal = 64 << 20, maxZIP = 80 << 20
    struct Item { var name: String; let data: Data }
    static func safeName(_ name: String) throws {
        guard !name.isEmpty, !name.contains("\\"), !name.contains("\0"), !name.contains(":"),
            !name.split(separator: "/", omittingEmptySubsequences: false).contains(where: { $0.isEmpty || $0 == "." || $0 == ".." }) else { throw Failure() }
    }
    private static func canonical(_ url: URL) -> URL {
        // resolvingSymlinksInPath alone does not resolve a missing output leaf.
        var existing = url.standardizedFileURL, missing: [String] = []
        while existing.path != "/" && !FileManager.default.fileExists(atPath: existing.path) {
            missing.append(existing.lastPathComponent)
            existing.deleteLastPathComponent()
        }
        var resolved = existing.resolvingSymlinksInPath()
        for component in missing.reversed() { resolved.appendPathComponent(component) }
        return resolved
    }
    private static func noLink(_ url: URL) throws -> URL {
        guard url.isFileURL else { throw Failure() }
        let path = url.standardizedFileURL
        let result = canonical(path.deletingLastPathComponent()).appendingPathComponent(path.lastPathComponent)
        var info = stat()
        if lstat(result.path, &info) == 0 && (info.st_mode & S_IFMT) == S_IFLNK { throw Failure() }
        return result
    }
    static func read(_ url: URL, limit: Int) throws -> Data {
        let handle = open(url.path, O_RDONLY | O_NOFOLLOW | O_NONBLOCK)
        guard handle >= 0 else { throw Failure() }; defer { close(handle) }
        var info = stat()
        guard fstat(handle, &info) == 0, (info.st_mode & S_IFMT) == S_IFREG, info.st_size <= limit else { throw Failure() }
        var data = Data(), buffer = [UInt8](repeating: 0, count: 8192)
        while true {
            let count = Darwin.read(handle, &buffer, buffer.count)
            if count < 0 { if errno == EINTR { continue }; throw Failure() }
            if count == 0 { break }
            guard data.count + count <= limit else { throw Failure() }; data.append(contentsOf: buffer.prefix(count))
        }
        return data
    }
    private static func source(_ source: URL) throws -> [Item] {
        let root = try noLink(source)
        var directory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: root.path, isDirectory: &directory) else { throw Failure() }
        var files: [Item] = [], total = 0, directories = 0
        func walk(_ folder: URL, _ prefix: String, _ depth: Int) throws {
            directories += 1; guard depth <= 128, directories <= 4096 else { throw Failure() }
            // Foundation directory enumeration decomposes Unicode names on Apple platforms.
            // Read raw directory names so revisions retain Go's byte-level filename identity.
            guard let directory = opendir(folder.path) else { throw Failure() }
            defer { closedir(directory) }
            while true {
                errno = 0
                guard let entry = readdir(directory) else {
                    guard errno == 0 else { throw Failure() }; break
                }
                let component = withUnsafePointer(to: &entry.pointee.d_name) {
                    $0.withMemoryRebound(to: CChar.self, capacity: Int(entry.pointee.d_namlen) + 1) { String(validatingUTF8: $0) }
                }
                guard let component = component else { throw Failure() }
                if component == "." || component == ".." { continue }
                let child = try noLink(folder.appendingPathComponent(component)), name = prefix + component
                try safeName(name)
                var info = stat()
                guard lstat(child.path, &info) == 0 else { throw Failure() }
                if (info.st_mode & S_IFMT) == S_IFDIR { try walk(child, name + "/", depth + 1) }
                else {
                    guard files.count < maxFiles else { throw Failure() }
                    let data = try read(child, limit: min(maxFile, maxTotal - total)); total += data.count
                    files.append(Item(name: name, data: data))
                }
            }
        }
        if directory.boolValue { try walk(root, "", 0) }
        else {
            guard ["zip", "xpi"].contains(root.pathExtension.lowercased()) else { throw Failure() }
            files = try ExtensionZIP.decode(read(root, limit: maxZIP))
        }
        guard !files.isEmpty, files.count <= maxFiles else { throw Failure() }
        if !files.contains(where: { $0.name == "manifest.json" }) {
            let parts = files[0].name.split(separator: "/")
            guard parts.count > 1 else { throw Failure() }
            let prefix = String(parts[0]) + "/"
            for i in files.indices {
                guard GoText.hasPrefix(files[i].name, prefix) else { throw Failure() }
                files[i].name = String(decoding: files[i].name.utf8.dropFirst(prefix.utf8.count), as: UTF8.self)
            }
        }
        files.sort { $0.name.utf8.lexicographicallyPrecedes($1.name.utf8) }
        var names = Set<Data>(); total = 0
        for file in files {
            try safeName(file.name)
            guard names.insert(Data(file.name.utf8)).inserted else { throw Failure() }
            let parts = file.name.split(separator: "/")
            if parts.count > 1 { for i in 1..<parts.count { guard !names.contains(Data(parts.prefix(i).joined(separator: "/").utf8)) else { throw Failure() } } }
            total += file.data.count; guard file.data.count <= maxFile, total <= maxTotal else { throw Failure() }
        }
        guard names.contains(Data("manifest.json".utf8)) else { throw Failure() }
        return files
    }
    private struct Manifest: Decodable {
        let manifest_version: Int, name: String, version: String
        let key: String?, permissions: [String]?, host_permissions: [String]?
    }
    private static func describe(_ files: [Item]) throws -> Description {
        guard let data = files.first(where: { $0.name == "manifest.json" })?.data, data.count <= 1 << 20 else { throw Failure() }
        let m: Manifest
        do { m = try JSONDecoder().decode(Manifest.self, from: data) } catch { throw Failure() }
        guard [2, 3].contains(m.manifest_version), !GoText.trimSpace(m.name).isEmpty, !GoText.trimSpace(m.version).isEmpty,
            m.name.utf8.count <= 256, m.version.utf8.count <= 64, (m.permissions?.count ?? 0) <= 256, (m.host_permissions?.count ?? 0) <= 256 else { throw Failure() }
        var id = ""
        if let key = m.key, !key.isEmpty {
            guard let bytes = Data(base64Encoded: key.replacingOccurrences(of: "\r", with: "").replacingOccurrences(of: "\n", with: "")), !bytes.isEmpty else { throw Failure() }
            for byte in SHA256.hash(data: bytes).prefix(16) { id += String(UnicodeScalar(97 + UInt32(byte >> 4))!) + String(UnicodeScalar(97 + UInt32(byte & 15))!) }
        }
        var hash = SHA256(), total = 0
        for file in files {
            hash.update(data: Data(file.name.utf8)); hash.update(data: Data([0]))
            var size = UInt64(file.data.count).bigEndian
            withUnsafeBytes(of: &size) { hash.update(data: Data($0)) }
            hash.update(data: file.data); total += file.data.count
        }
        func unique(_ values: [String]?) -> [String] {
            Set((values ?? []).filter { !$0.isEmpty }.map { Data($0.utf8) }).sorted { $0.lexicographicallyPrecedes($1) }.map { String(decoding: $0, as: UTF8.self) }
        }
        return Description(name: m.name, version: m.version, manifestVersion: m.manifest_version, id: id,
            permissions: unique(m.permissions), hostPermissions: unique(m.host_permissions), files: files.count, bytes: total,
            revision: "sha256:" + hash.finalize().map { String(format: "%02x", $0) }.joined())
    }
    public static func inspect(_ source: URL) throws -> Description { try describe(self.source(source)) }
    public static func checkedSource(_ source: URL, revision: String) throws -> Description {
        guard !revision.isEmpty else { throw Failure() }
        let d = try inspect(source); guard d.revision == revision else { throw Failure() }; return d
    }
    public static func outsideSource(_ source: URL, output: URL) throws {
        guard source.isFileURL, output.isFileURL else { throw Failure() }
        let root = canonical(source).path
        let destination = canonical(output).path
        guard destination != root, !GoText.hasPrefix(destination, root + "/") else { throw Failure() }
    }
    private static func output(_ source: URL, _ destination: URL) throws -> URL {
        try outsideSource(source, output: destination)
        let target = try noLink(destination); var info = stat()
        guard lstat(target.path, &info) != 0, errno == ENOENT else { throw Failure() }
        try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
        return target
    }
    public static func unusedArtifact(_ destination: URL, extension suffix: String) throws {
        guard destination.isFileURL, ("." + destination.pathExtension).lowercased() == suffix.lowercased() else { throw Failure() }
        let target = try noLink(destination); var info = stat()
        guard lstat(target.path, &info) != 0, errno == ENOENT else { throw Failure() }
        try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
    }
    /// A deterministic stored ZIP. Revision equality is independent of compression/ZIP metadata.
    public static func packageArchive(_ source: URL, output destination: URL) throws -> Description {
        let files = try self.source(source), d = try describe(files), target = try output(source, destination)
        let archive = try ExtensionZIP.encode(files)
        guard try describe(ExtensionZIP.decode(archive)).revision == d.revision else { throw Failure() }
        try writeExclusive(target, archive); return d
    }
    public static func stage(_ source: URL, destination: URL, expectedRevision: String = "") throws -> Description {
        let files = try self.source(source), d = try describe(files)
        guard expectedRevision.isEmpty || expectedRevision == d.revision else { throw Failure() }
        let target = try output(source, destination)
        guard mkdir(target.path, 0o755) == 0 else { throw Failure() }
        var complete = false; defer { if !complete { try? FileManager.default.removeItem(at: target) } }
        for file in files {
            // URL.path decomposes Unicode on Apple platforms. Keep archive-relative
            // names as raw UTF-8 strings when materializing them on disk.
            let parts = file.name.split(separator: "/")
            var parent = target.path
            for part in parts.dropLast() {
                parent += "/" + part
                if mkdir(parent, 0o755) != 0 {
                    var info = stat()
                    guard errno == EEXIST, lstat(parent, &info) == 0,
                        (info.st_mode & S_IFMT) == S_IFDIR else { throw Failure() }
                }
            }
            try createExclusive(parent + "/" + parts.last!) { fd in try write(fd, file.data) }
        }
        guard try inspect(target).revision == d.revision else { throw Failure() }; complete = true; return d
    }
    private static func writeExclusive(_ file: URL, _ data: Data) throws {
        try createExclusive(file.path) { fd in try write(fd, data) }
    }
    private static func createExclusive(_ path: String, body: (Int32) throws -> Void) throws {
        let fd = open(path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o644)
        guard fd >= 0 else { throw Failure() }
        var complete = false, closed = false
        defer { if !closed { close(fd) }; if !complete { unlink(path) } }
        try body(fd)
        guard fsync(fd) == 0 else { throw Failure() }
        let status = close(fd); closed = true
        guard status == 0 else { throw Failure() }; complete = true
    }
    private static func write(_ fd: Int32, _ data: Data) throws {
        try data.withUnsafeBytes { bytes in
            var at = 0
            while at < bytes.count {
                let n = Darwin.write(fd, bytes.baseAddress!.advanced(by: at), bytes.count - at)
                if n < 0 { if errno == EINTR { continue }; throw Failure() }
                guard n > 0 else { throw Failure() }; at += n
            }
        }
    }
    private static func readChunks(_ source: URL, body: (Data) throws -> Void) throws {
        guard source.isFileURL else { throw Failure() }
        let fd = open(source.path, O_RDONLY | O_NONBLOCK)
        guard fd >= 0 else { throw Failure() }; defer { close(fd) }
        var info = stat()
        guard fstat(fd, &info) == 0, (info.st_mode & S_IFMT) == S_IFREG else { throw Failure() }
        var buffer = [UInt8](repeating: 0, count: 8192)
        while true {
            let n = Darwin.read(fd, &buffer, buffer.count)
            if n < 0 { if errno == EINTR { continue }; throw Failure() }
            if n == 0 { return }
            try body(Data(buffer.prefix(n)))
        }
    }
    public static func copyArtifactExclusive(_ source: URL, destination: URL) throws {
        guard source.isFileURL, destination.isFileURL else { throw Failure() }
        try createExclusive(destination.path) { fd in
            try readChunks(source) { try write(fd, $0) }
        }
    }
    public static func fileSHA256(_ url: URL) throws -> String {
        var hash = SHA256()
        try readChunks(url) { hash.update(data: $0) }
        return "sha256:" + hash.finalize().map { String(format: "%02x", $0) }.joined()
    }
}
