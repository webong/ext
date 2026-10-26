import Foundation
import CryptoKit
import Darwin

/// Go-compatible persistence in a caller-owned private directory. Calls across store instances
/// are serialized within this process. The host must exclude other processes from writing it.
public final class UserscriptStore {
    public enum StoreError: Error { case invalidIdentity, unsafeFile, missing, exists, oversized }
    public static let maxRecordBytes = 8 << 20
    private static let lock = NSRecursiveLock()
    private struct Watch { let folder: URL; let changed: () -> Void }
    private static var watches: [UUID: Watch] = [:]
    public final class Observation {
        private let id: UUID
        fileprivate init(_ id: UUID) { self.id = id }
        public func cancel() {
            UserscriptStore.lock.lock(); defer { UserscriptStore.lock.unlock() }
            UserscriptStore.watches.removeValue(forKey: id)
        }
        deinit { cancel() }
    }
    /// Observe successful in-process writes to this directory/target, across store instances.
    /// Callbacks run on the writer's thread, outside the store lock; dispatch UI work yourself.
    /// Cancellation can race an already-dispatched callback. No file contents are delivered.
    public func observe(target: String, onChange: @escaping () -> Void) throws -> Observation {
        try locked {
            let id = UUID()
            Self.watches[id] = Watch(folder: try folder(target), changed: onChange)
            return Observation(id)
        }
    }
    private func publish(_ target: String) {
        let callbacks: [() -> Void] = locked {
            guard let folder = try? folder(target) else { return [] }
            return Self.watches.values.filter { $0.folder == folder }.map { $0.changed }
        }
        callbacks.forEach { $0() }
    }
    private let directory: URL
    public init(directory: URL) { self.directory = directory.standardizedFileURL.resolvingSymlinksInPath() }
    private func locked<T>(_ body: () throws -> T) rethrows -> T {
        Self.lock.lock(); defer { Self.lock.unlock() }; return try body()
    }
    private func folder(_ target: String) throws -> URL {
        guard !GoText.isBlank(target), target == GoText.trimSpace(target), target.utf8.count <= 512 else { throw StoreError.invalidIdentity }
        let hash = SHA256.hash(data: Data(target.utf8)).map { String(format: "%02x", $0) }.joined()
        return directory.appendingPathComponent(hash, isDirectory: true)
    }
    private func path(_ target: String, _ id: String) throws -> URL {
        guard id.range(of: "^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$", options: .regularExpression) != nil,
              !id.contains("\n") else { throw StoreError.invalidIdentity }
        return try folder(target).appendingPathComponent(id + ".json")
    }
    private func check(_ url: URL, directory: Bool, missing: Bool = false) throws -> Bool {
        let attributes: [FileAttributeKey: Any]
        do { attributes = try FileManager.default.attributesOfItem(atPath: url.path) }
        catch let error as NSError where error.domain == NSCocoaErrorDomain && error.code == NSFileReadNoSuchFileError {
            if missing { return false }; throw StoreError.missing
        }
        guard attributes[.type] as? FileAttributeType == (directory ? .typeDirectory : .typeRegular) else { throw StoreError.unsafeFile }
        return true
    }
    public func load(target: String, id: String) throws -> UserscriptRecord {
        try locked {
            let file = try path(target, id)
            _ = try check(directory, directory: true)
            _ = try check(file.deletingLastPathComponent(), directory: true)
            _ = try check(file, directory: false)
            let attributes = try FileManager.default.attributesOfItem(atPath: file.path)
            guard let size = attributes[.size] as? NSNumber, size.intValue <= Self.maxRecordBytes else { throw StoreError.oversized }
            let data = try Data(contentsOf: file)
            guard data.count <= Self.maxRecordBytes, let json = String(data: data, encoding: .utf8) else { throw StoreError.oversized }
            _ = try ManagementJSON.read(json, object: true)
            let record = try JSONDecoder().decode(UserscriptRecord.self, from: data)
            guard record.target == target, record.id == id else { throw StoreError.invalidIdentity }
            try Userscript.validate(record); return record
        }
    }
    public func list(target: String) throws -> [UserscriptRecord] {
        try locked {
            let folder = try folder(target)
            guard try check(directory, directory: true, missing: true),
                  try check(folder, directory: true, missing: true) else { return [] }
            return try FileManager.default.contentsOfDirectory(atPath: folder.path).filter { $0.hasSuffix(".json") }.sorted().map {
                try load(target: target, id: String($0.dropLast(5)))
            }
        }
    }
    public func save(_ record: UserscriptRecord, replace: Bool = false) throws {
        try locked { try persist(record, replace: replace) }
        publish(record.target)
    }
    private func persist(_ record: UserscriptRecord, replace: Bool) throws {
        try Userscript.validate(record)
        let file = try path(record.target, record.id)
        let folder = file.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        _ = try check(directory, directory: true)
        if !(try check(folder, directory: true, missing: true)) {
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        }
        let exists = try check(file, directory: false, missing: true)
        if exists && !replace { throw StoreError.exists }
        if !exists && replace { throw StoreError.missing }
        let data = try JSONEncoder().encode(record)
        guard data.count <= Self.maxRecordBytes else { throw StoreError.oversized }
        let temporary = folder.appendingPathComponent(".userscript-" + UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: temporary) }
        let descriptor = open(temporary.path, O_WRONLY | O_CREAT | O_EXCL, 0o600)
        guard descriptor >= 0 else { throw StoreError.unsafeFile }
        do {
            try data.withUnsafeBytes { buffer in
                var offset = 0
                while offset < buffer.count {
                    let count = Darwin.write(descriptor, buffer.baseAddress!.advanced(by: offset), buffer.count - offset)
                    if count < 0 && errno == EINTR { continue }
                    guard count > 0 else { throw StoreError.unsafeFile }; offset += count
                }
            }
            guard fsync(descriptor) == 0 else { throw StoreError.unsafeFile }
        } catch { close(descriptor); throw error }
        guard close(descriptor) == 0 else { throw StoreError.unsafeFile }
        // rename is atomic on the local filesystems supported by Apple platforms.
        guard rename(temporary.path, file.path) == 0 else { throw StoreError.unsafeFile }
    }
    @discardableResult public func setEnabled(target: String, id: String, enabled: Bool) throws -> UserscriptRecord {
        let record = try locked {
            var record = try load(target: target, id: id); record.enabled = enabled
            try persist(record, replace: true); return record
        }
        publish(target); return record
    }
    public func remove(target: String, id: String) throws {
        try locked { _ = try load(target: target, id: id); try FileManager.default.removeItem(at: path(target, id)) }
        publish(target)
    }
}
