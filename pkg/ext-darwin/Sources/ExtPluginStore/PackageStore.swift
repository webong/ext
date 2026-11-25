import CExtEngine
import Foundation

/// Why the engine refused a package store call. An outcome is a status and never
/// text: the engine does not describe the path or the package that failed.
public enum PackageStoreError: Error, Equatable {
    /// A package, id or directory that is not valid, a source that is not a package,
    /// an artifact over a limit, or a root set the layered view does not accept.
    case invalid
    /// A package the caller has not reviewed: an empty digest, or one that is not the
    /// package's.
    case denied
    /// An artifact whose digest differs, a directory that does not match its
    /// manifest, or an upgrade that changes the id or the contracts.
    case mismatch
    /// A package that does not serve the contract asked for, or has no entrypoint that
    /// fits the protocol and the platform.
    case unsupported
    /// An id that is not installed.
    case notFound
    /// An install over an installed id.
    case exists
    /// A file or directory that is not there: a package source, its manifest or an
    /// artifact it lists. Other failures to read are `io`.
    case missing
    /// A file or directory that could not be read or written.
    case io
    /// Any other engine status.
    case engine(Int32)

    /// The error for an engine status, nil for success. This is how to read the
    /// `status` of a `verify` or `listUnverified` entry.
    public init?(status: Int32) {
        switch status {
        case ext_plugin_status(EXT_PLUGIN_OK): return nil
        case ext_plugin_status(EXT_PLUGIN_INVALID): self = .invalid
        case ext_plugin_status(EXT_PLUGIN_DENIED): self = .denied
        case ext_plugin_status(EXT_PLUGIN_MISMATCH): self = .mismatch
        case ext_plugin_status(EXT_PLUGIN_UNSUPPORTED): self = .unsupported
        case ext_plugin_status(EXT_PLUGIN_NOT_FOUND): self = .notFound
        case ext_plugin_status(EXT_PLUGIN_EXISTS): self = .exists
        case ext_plugin_status(EXT_PLUGIN_MISSING): self = .missing
        case ext_plugin_status(EXT_PLUGIN_IO): self = .io
        default: self = .engine(status)
        }
    }
}

/// Bounds on one artifact file (a file inside a tree counts too), on the files in
/// one tree and on the bytes of one tree, and on an archive file. Zero takes the
/// engine's default: 4 GiB, 250000 files, 16 GiB and 512 MiB. The first three bound what
/// `install`, `upgrade`, `replace` and `installArchive` copy (for an archive, every entry
/// counts toward `maxTreeFiles` and the extracted total toward `maxTreeBytes`);
/// `maxArchiveBytes` bounds the archive `installArchive` opens. Loading and the reports
/// read a package as it is and are not bounded by them.
public struct PackageStoreLimits: Equatable {
    public var maxArtifactBytes: UInt64
    public var maxTreeFiles: UInt32
    public var maxTreeBytes: UInt64
    public var maxArchiveBytes: UInt64

    public init(maxArtifactBytes: UInt64 = 0, maxTreeFiles: UInt32 = 0, maxTreeBytes: UInt64 = 0,
                maxArchiveBytes: UInt64 = 0) {
        self.maxArtifactBytes = maxArtifactBytes
        self.maxTreeFiles = maxTreeFiles
        self.maxTreeBytes = maxTreeBytes
        self.maxArchiveBytes = maxArchiveBytes
    }

    /// The engine's bound on an archive file when `maxArchiveBytes` is zero.
    public static let defaultMaxArchiveBytes: UInt64 = 512 << 20

    fileprivate var raw: ext_plugin_store_limits {
        ext_plugin_store_limits(max_artifact_bytes: maxArtifactBytes, max_tree_files: maxTreeFiles, max_tree_bytes: maxTreeBytes)
    }
}

/// What an archive install recorded for a package: its id, the revision the install
/// produced and the archive's SHA-256 in lower case. It describes the install that wrote
/// it, so compare `revision` with the installed package before relying on it.
public struct ArchiveReceipt: Equatable, Decodable {
    public let id: String
    public let revision: String
    public let artifactSHA256: String

    public init(id: String, revision: String, artifactSHA256: String) {
        self.id = id
        self.revision = revision
        self.artifactSHA256 = artifactSHA256
    }

    private enum CodingKeys: String, CodingKey {
        case id, revision
        case artifactSHA256 = "artifact_sha256"
    }
}

/// The entrypoint `PackageStore.resolveEntrypoint` chose: the entrypoint's name, its
/// artifact's name, the artifact's path under the store and the package digest.
public struct ResolvedEntrypoint: Equatable, Decodable {
    public let name: String
    public let artifact: String
    public let path: String
    public let digest: String

    public init(name: String, artifact: String, path: String, digest: String) {
        self.name = name
        self.artifact = artifact
        self.path = path
        self.digest = digest
    }
}

/// Owns EXT_HOME's native cross-process package writer lock. Acquisition fails
/// immediately on contention; stale locks are never recovered by age.
public final class PackageWriter: @unchecked Sendable {
    private let mutex = NSLock()
    private var handle: OpaquePointer?

    public init(home: String) throws {
        var opened: OpaquePointer?
        try withEngineString(home) { try check(ext_plugin_store_writer_acquire($0, &opened)) }
        handle = opened
    }

    /// Releases once. A replacement lock is preserved and reported as mismatch.
    public func close() throws {
        mutex.lock()
        defer { mutex.unlock() }
        guard let owned = handle else { return }
        handle = nil
        let status = ext_plugin_store_writer_release(owned)
        ext_plugin_store_writer_free(owned)
        try check(status)
    }

    deinit { try? close() }
}

/// The engine's package store: install, upgrade, replace, remove and load reviewed
/// `ext.package/v1` packages under a root the application supplies (see `ext_store_*`
/// in lib/plugin/include/ext_plugin_host.h). A package is its `plugin.json` manifest and the
/// artifacts it lists, in a directory named by the package id with `/` written `%2f`.
/// The store copies and re-verifies content; it never runs it or grants authority, and
/// it has no default location. A missing root is an empty store, made (owner-only) by
/// the first install. The engine serializes calls on one store; several processes
/// mutating one root coordinate through withWriter(home:) or PackageWriter.
///
/// Failures are `PackageStoreError`s. The whole-root reports (`list`, `verify`,
/// `listUnverified`) return the engine's JSON as it is, as `load` returns `plugin.json`,
/// so a caller decodes it with its own types; the shapes are in `ext_plugin_store_list`.
public final class PackageStore: @unchecked Sendable {
    private let handle: OpaquePointer
    /// The store's limits, `maxArchiveBytes` included, as it was opened with them.
    public let limits: PackageStoreLimits

    /// Returns a store rooted at `root`, which need not exist yet.
    public init(root: String, limits: PackageStoreLimits = PackageStoreLimits()) throws {
        var raw = limits.raw
        var opened: OpaquePointer?
        try withEngineString(root) { try check(ext_plugin_store_open($0, &raw, &opened)) }
        guard let opened else { throw PackageStoreError.engine(ext_plugin_status(EXT_PLUGIN_NOMEM)) }
        handle = opened
        self.limits = limits
    }

    deinit { ext_plugin_store_close(handle) }

    /// Hold the native writer across all reads, dependency checks and updates in
    /// the callback. Pass EXT_HOME, not the store directory. Unwinding releases it.
    public func withWriter<T>(home: String, _ operation: (PackageStore) throws -> T) throws -> T {
        let writer = try PackageWriter(home: home)
        let result: T
        do { result = try operation(self) }
        catch { try? writer.close(); throw error }
        try writer.close()
        return result
    }

    private typealias Put = (OpaquePointer?, UnsafePointer<CChar>?, UInt32, UnsafeMutablePointer<ext_plugin_buffer>?) -> ext_plugin_status

    private func put(_ call: Put, _ source: String, _ preserveExecutable: Bool) throws -> Data {
        let flags = preserveExecutable ? UInt32(EXT_PLUGIN_STORE_PRESERVE_EXECUTABLE) : 0
        return try withEngineString(source) { source in
            try output { call(handle, source, flags, $0) }
        }
    }

    /// Copies the package in the directory `source` into the store, never over an
    /// installed id, and returns the installed `plugin.json`, byte for byte as the
    /// source had it. With `preserveExecutable` a file artifact that is executable in the
    /// source stays so; entrypoints and executable files in trees always do.
    @discardableResult
    public func install(_ source: String, preserveExecutable: Bool = false) throws -> Data {
        try put(ext_plugin_store_install, source, preserveExecutable)
    }

    /// Replaces an installed package with a new revision of the same id and contracts.
    /// The previous installation survives any failure.
    @discardableResult
    public func upgrade(_ source: String, preserveExecutable: Bool = false) throws -> Data {
        try put(ext_plugin_store_upgrade, source, preserveExecutable)
    }

    /// Installs or replaces the package of the source's id, whatever its contracts.
    @discardableResult
    public func replace(_ source: String, preserveExecutable: Bool = false) throws -> Data {
        try put(ext_plugin_store_replace, source, preserveExecutable)
    }

    /// Installs the package in the archive file at `archive`, or upgrades the installed
    /// package of the same id to it (as `upgrade` does), and returns the installed
    /// `plugin.json`. The archive is a zip, a gzip-compressed tar or a plain tar, told apart
    /// by its content, and must have the SHA-256 `sha256` (64 hexadecimal characters in
    /// either case); it is checked before anything is extracted. With `id` the archive must
    /// hold that package. `plugin.json` is at the top of the archive or in its one top-level
    /// directory, and only directories and regular files are extracted, under the store's
    /// limits; the archive file itself is bounded by `limits.maxArchiveBytes`. A package
    /// already installed at the archive's revision must have the archive's content.
    ///
    /// A successful install leaves a receipt (`receipt`). When the receipt of the installed
    /// package names `sha256` and the revision installed now, and the package still
    /// verifies, the call returns it without opening the archive.
    ///
    /// Failures besides those of `install` and `upgrade`: `mismatch` for a digest that
    /// differs, another package than `id`, or other content at the installed revision;
    /// `unsupported` for a format that is not read (zstd, xz, bzip2); `invalid` for a
    /// missing or malformed digest, a zip64 archive, an archive over a limit, a link or
    /// special entry, a path that escapes, is absolute, repeats another in any case or could
    /// not exist on Windows; `missing` for an archive that is not there. Getting the archive
    /// is the application's: `ArchiveDownloader` fetches one over HTTPS.
    @discardableResult
    public func installArchive(_ archive: String, sha256: String, id: String? = nil,
                               preserveExecutable: Bool = false) throws -> Data {
        let flags = preserveExecutable ? UInt32(EXT_PLUGIN_STORE_PRESERVE_EXECUTABLE) : 0
        let maxArchiveBytes = limits.maxArchiveBytes
        return try withEngineStrings([archive, sha256, id]) { s in
            try output { ext_plugin_store_install_archive(handle, s[0], s[1], s[2], flags, maxArchiveBytes, $0) }
        }
    }

    /// The receipt the last archive install of `id` left. `PackageStoreError.notFound` when
    /// no archive install recorded one; `remove` deletes it.
    public func receipt(_ id: String) throws -> ArchiveReceipt {
        let json = try withEngineString(id) { id in try output { ext_plugin_store_receipt(handle, id, $0) } }
        guard let receipt = try? JSONDecoder().decode(ArchiveReceipt.self, from: json) else {
            throw PackageStoreError.invalid
        }
        return receipt
    }

    /// Deletes one installed package and its archive receipt.
    public func remove(_ id: String) throws {
        try withEngineString(id) { try check(ext_plugin_store_remove(handle, $0)) }
    }

    /// Reads and verifies one installed package and returns its `plugin.json`.
    public func load(_ id: String) throws -> Data {
        try withEngineString(id) { id in try output { ext_plugin_store_load(handle, id, $0) } }
    }

    /// The installed package ids in byte order, without reading or verifying any
    /// package. Staging directories are skipped; any other entry that is not a package
    /// directory is `PackageStoreError.invalid`.
    public func ids() throws -> [String] {
        let json = try output { ext_plugin_store_ids(handle, $0) }
        guard let ids = try? JSONDecoder().decode([String].self, from: json) else { throw PackageStoreError.invalid }
        return ids
    }

    /// Reads and verifies every package and returns a JSON array of
    /// `{id, directory, layer, manifest}` sorted by id, the manifest being `plugin.json`
    /// as an embedded value. It fails with the status of the first entry that is not a
    /// package directory and of the first package that does not verify.
    public func list() throws -> Data {
        try output { ext_plugin_store_list(handle, $0) }
    }

    /// Checks every entry without stopping at a bad one and returns a JSON array of
    /// `{id, directory, layer, status}`, where `status` is the engine status verifying it
    /// gave (0 for verified; `PackageStoreError(status:)` names the rest). It runs
    /// nothing and changes nothing.
    public func verify() throws -> Data {
        try output { ext_plugin_store_verify(handle, $0) }
    }

    /// Reads only the manifests and checks them against their directory names; no
    /// artifact is read or hashed, so THE RESULT IS UNVERIFIED and must not be launched
    /// or trusted as it stands. Returns `{"packages": [...], "problems": [...]}` as JSON;
    /// a package that cannot be read does not hide the others.
    public func listUnverified() throws -> Data {
        try output { ext_plugin_store_list_unverified(handle, $0) }
    }

    /// Chooses the entrypoint the installed package `id` offers for `protocol` on a
    /// platform, for a package the caller has reviewed (see `ext_plugin_store_resolve_entrypoint`
    /// in lib/plugin/include/ext_plugin_host.h for the rules). The package is read and verified
    /// whole and must serve the exact `contract` and `version`; it is chosen only when its
    /// directory digest equals `reviewedDigest` (lowercase hex, as recorded at review), so
    /// an empty or changed digest is `PackageStoreError.denied` and an installed package is
    /// never trusted for being installed. `PackageStoreError.unsupported` means the package
    /// does not serve the contract or has no entrypoint that fits. Nothing is run. `os` and
    /// `arch` are Go's `GOOS` and `GOARCH` names; nil is the platform the engine was built
    /// for.
    public func resolveEntrypoint(id: String, contract: String, version: String, protocol wireProtocol: String,
                                  reviewedDigest: String, os: String? = nil,
                                  arch: String? = nil) throws -> ResolvedEntrypoint {
        let json = try withEngineStrings([id, contract, version, wireProtocol, reviewedDigest, os, arch]) { s in
            try output { ext_plugin_store_resolve_entrypoint(handle, s[0], s[1], s[2], s[3], s[4], s[5], s[6], $0) }
        }
        guard let entrypoint = try? JSONDecoder().decode(ResolvedEntrypoint.self, from: json) else {
            throw PackageStoreError.invalid
        }
        return entrypoint
    }
}

/// A package served by a `LayeredPackageStore`: its `plugin.json` and the position of
/// the root that holds it.
public struct ServedPackage: Equatable {
    public let manifest: Data
    public let layer: Int

    public init(manifest: Data, layer: Int) {
        self.manifest = manifest
        self.layer = layer
    }
}

/// A read-only view over several roots in priority order, highest first, such as a user
/// root before a system root. An id is served from the first root that holds it; a copy
/// in a lower root is shadowed and never read. If the winning copy does not verify,
/// `load` and the reports fail instead of falling back to a lower one. The view never
/// writes, so a system root is never created or modified through it; install, upgrade
/// and remove stay with the `PackageStore` the caller owns. It has no default roots.
/// Roots are compared as cleaned paths: a repeated root, an empty one, or no root at all
/// is `PackageStoreError.invalid`. The reports are those of `PackageStore` over all the
/// roots, one line per id from the root that serves it.
public final class LayeredPackageStore: @unchecked Sendable {
    private let handle: OpaquePointer

    /// Returns a view over `roots`, which need not exist.
    public init(roots: [String], limits: PackageStoreLimits = PackageStoreLimits()) throws {
        var raw = limits.raw
        var owned: [UnsafeMutablePointer<CChar>] = []
        defer { owned.forEach { free($0) } }
        for root in roots {
            guard !root.utf8.contains(0) else { throw PackageStoreError.invalid }
            guard let copy = strdup(root) else { throw PackageStoreError.engine(ext_plugin_status(EXT_PLUGIN_NOMEM)) }
            owned.append(copy)
        }
        let pointers: [UnsafePointer<CChar>?] = owned.map { UnsafePointer($0) }
        var opened: OpaquePointer?
        try check(ext_plugin_layered_open(pointers, pointers.count, &raw, &opened))
        guard let opened else { throw PackageStoreError.engine(ext_plugin_status(EXT_PLUGIN_NOMEM)) }
        handle = opened
    }

    deinit { ext_plugin_layered_close(handle) }

    /// Reads and verifies the package from the first root that holds `id` and returns its
    /// `plugin.json` and the position of that root. `PackageStoreError.notFound` means no
    /// root holds it.
    public func load(_ id: String) throws -> ServedPackage {
        var layer = 0
        let manifest = try withEngineString(id) { id in try output { ext_plugin_layered_load(handle, id, $0, &layer) } }
        return ServedPackage(manifest: manifest, layer: layer)
    }

    /// As `PackageStore.list` over every root, one line per id from the root that serves it.
    public func list() throws -> Data {
        try output { ext_plugin_layered_list(handle, $0) }
    }

    /// As `PackageStore.verify` over every root.
    public func verify() throws -> Data {
        try output { ext_plugin_layered_verify(handle, $0) }
    }

    /// As `PackageStore.listUnverified` over every root; the result is unverified.
    public func listUnverified() throws -> Data {
        try output { ext_plugin_layered_list_unverified(handle, $0) }
    }
}

private func check(_ status: ext_plugin_status) throws {
    if let error = PackageStoreError(status: status) { throw error }
}

/// Runs `call` with an output buffer, takes its bytes and frees the engine's copy.
private func output(_ call: (UnsafeMutablePointer<ext_plugin_buffer>) -> ext_plugin_status) throws -> Data {
    var out = ext_plugin_buffer(data: nil, len: 0)
    defer { ext_plugin_buffer_free(&out) }
    try check(call(&out))
    guard let bytes = out.data else { return Data() }
    return Data(bytes: bytes, count: out.len)
}

/// A C string for the engine. One with a NUL in it would be cut short there and name
/// another path, so it is refused instead.
private func withEngineString<R>(_ string: String, _ body: (UnsafePointer<CChar>) throws -> R) throws -> R {
    guard !string.utf8.contains(0) else { throw PackageStoreError.invalid }
    return try string.withCString(body)
}

/// C strings for the engine, in order; a nil string is a NULL pointer.
private func withEngineStrings<R>(_ strings: [String?], _ body: ([UnsafePointer<CChar>?]) throws -> R) throws -> R {
    func walk(_ index: Int, _ pointers: [UnsafePointer<CChar>?]) throws -> R {
        guard index < strings.count else { return try body(pointers) }
        guard let string = strings[index] else { return try walk(index + 1, pointers + [nil]) }
        return try withEngineString(string) { try walk(index + 1, pointers + [$0]) }
    }
    return try walk(0, [])
}
