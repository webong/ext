import CExtEngine
import Foundation

/// Why the engine refused a package store call. An outcome is a status and never
/// text: the engine does not describe the path or the package that failed.
public enum PackageStoreError: Error, Equatable {
    /// A package, id or directory that is not valid, a source that is not a package,
    /// an artifact over a limit, or a root set the layered view does not accept.
    case invalid
    /// An artifact whose digest differs, a directory that does not match its
    /// manifest, or an upgrade that changes the id or the contracts.
    case mismatch
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
        case ext_status(EXT_OK): return nil
        case ext_status(EXT_INVALID): self = .invalid
        case ext_status(EXT_MISMATCH): self = .mismatch
        case ext_status(EXT_NOT_FOUND): self = .notFound
        case ext_status(EXT_EXISTS): self = .exists
        case ext_status(EXT_MISSING): self = .missing
        case ext_status(EXT_IO): self = .io
        default: self = .engine(status)
        }
    }
}

/// Bounds on one artifact file (a file inside a tree counts too), on the files in
/// one tree and on the bytes of one tree. Zero takes the engine's default: 4 GiB,
/// 250000 files and 16 GiB. They bound what `install`, `upgrade` and `replace` copy;
/// loading and the reports read a package as it is and are not bounded by them.
public struct PackageStoreLimits: Equatable {
    public var maxArtifactBytes: UInt64
    public var maxTreeFiles: UInt32
    public var maxTreeBytes: UInt64

    public init(maxArtifactBytes: UInt64 = 0, maxTreeFiles: UInt32 = 0, maxTreeBytes: UInt64 = 0) {
        self.maxArtifactBytes = maxArtifactBytes
        self.maxTreeFiles = maxTreeFiles
        self.maxTreeBytes = maxTreeBytes
    }

    fileprivate var raw: ext_store_limits {
        ext_store_limits(max_artifact_bytes: maxArtifactBytes, max_tree_files: maxTreeFiles, max_tree_bytes: maxTreeBytes)
    }
}

/// The engine's package store: install, upgrade, replace, remove and load reviewed
/// `ext.package/v1` packages under a root the application supplies (see `ext_store_*`
/// in lib/plugin/include/ext_host.h). A package is its `plugin.json` manifest and the
/// artifacts it lists, in a directory named by the package id with `/` written `%2f`.
/// The store copies and re-verifies content; it never runs it or grants authority, and
/// it has no default location. A missing root is an empty store, made (owner-only) by
/// the first install. The engine serializes calls on one store; several processes
/// mutating one root are the application's to serialize.
///
/// Failures are `PackageStoreError`s. The whole-root reports (`list`, `verify`,
/// `listUnverified`) return the engine's JSON as it is, as `load` returns `plugin.json`,
/// so a caller decodes it with its own types; the shapes are in `ext_store_list`.
public final class PackageStore: @unchecked Sendable {
    private let handle: OpaquePointer

    /// Returns a store rooted at `root`, which need not exist yet.
    public init(root: String, limits: PackageStoreLimits = PackageStoreLimits()) throws {
        var raw = limits.raw
        var opened: OpaquePointer?
        try withEngineString(root) { try check(ext_store_open($0, &raw, &opened)) }
        guard let opened else { throw PackageStoreError.engine(ext_status(EXT_NOMEM)) }
        handle = opened
    }

    deinit { ext_store_close(handle) }

    private typealias Put = (OpaquePointer?, UnsafePointer<CChar>?, UInt32, UnsafeMutablePointer<ext_buffer>?) -> ext_status

    private func put(_ call: Put, _ source: String, _ preserveExecutable: Bool) throws -> Data {
        let flags = preserveExecutable ? UInt32(EXT_STORE_PRESERVE_EXECUTABLE) : 0
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
        try put(ext_store_install, source, preserveExecutable)
    }

    /// Replaces an installed package with a new revision of the same id and contracts.
    /// The previous installation survives any failure.
    @discardableResult
    public func upgrade(_ source: String, preserveExecutable: Bool = false) throws -> Data {
        try put(ext_store_upgrade, source, preserveExecutable)
    }

    /// Installs or replaces the package of the source's id, whatever its contracts.
    @discardableResult
    public func replace(_ source: String, preserveExecutable: Bool = false) throws -> Data {
        try put(ext_store_replace, source, preserveExecutable)
    }

    /// Deletes one installed package.
    public func remove(_ id: String) throws {
        try withEngineString(id) { try check(ext_store_remove(handle, $0)) }
    }

    /// Reads and verifies one installed package and returns its `plugin.json`.
    public func load(_ id: String) throws -> Data {
        try withEngineString(id) { id in try output { ext_store_load(handle, id, $0) } }
    }

    /// The installed package ids in byte order, without reading or verifying any
    /// package. Staging directories are skipped; any other entry that is not a package
    /// directory is `PackageStoreError.invalid`.
    public func ids() throws -> [String] {
        let json = try output { ext_store_ids(handle, $0) }
        guard let ids = try? JSONDecoder().decode([String].self, from: json) else { throw PackageStoreError.invalid }
        return ids
    }

    /// Reads and verifies every package and returns a JSON array of
    /// `{id, directory, layer, manifest}` sorted by id, the manifest being `plugin.json`
    /// as an embedded value. It fails with the status of the first entry that is not a
    /// package directory and of the first package that does not verify.
    public func list() throws -> Data {
        try output { ext_store_list(handle, $0) }
    }

    /// Checks every entry without stopping at a bad one and returns a JSON array of
    /// `{id, directory, layer, status}`, where `status` is the engine status verifying it
    /// gave (0 for verified; `PackageStoreError(status:)` names the rest). It runs
    /// nothing and changes nothing.
    public func verify() throws -> Data {
        try output { ext_store_verify(handle, $0) }
    }

    /// Reads only the manifests and checks them against their directory names; no
    /// artifact is read or hashed, so THE RESULT IS UNVERIFIED and must not be launched
    /// or trusted as it stands. Returns `{"packages": [...], "problems": [...]}` as JSON;
    /// a package that cannot be read does not hide the others.
    public func listUnverified() throws -> Data {
        try output { ext_store_list_unverified(handle, $0) }
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
            guard let copy = strdup(root) else { throw PackageStoreError.engine(ext_status(EXT_NOMEM)) }
            owned.append(copy)
        }
        let pointers: [UnsafePointer<CChar>?] = owned.map { UnsafePointer($0) }
        var opened: OpaquePointer?
        try check(ext_layered_open(pointers, pointers.count, &raw, &opened))
        guard let opened else { throw PackageStoreError.engine(ext_status(EXT_NOMEM)) }
        handle = opened
    }

    deinit { ext_layered_close(handle) }

    /// Reads and verifies the package from the first root that holds `id` and returns its
    /// `plugin.json` and the position of that root. `PackageStoreError.notFound` means no
    /// root holds it.
    public func load(_ id: String) throws -> ServedPackage {
        var layer = 0
        let manifest = try withEngineString(id) { id in try output { ext_layered_load(handle, id, $0, &layer) } }
        return ServedPackage(manifest: manifest, layer: layer)
    }

    /// As `PackageStore.list` over every root, one line per id from the root that serves it.
    public func list() throws -> Data {
        try output { ext_layered_list(handle, $0) }
    }

    /// As `PackageStore.verify` over every root.
    public func verify() throws -> Data {
        try output { ext_layered_verify(handle, $0) }
    }

    /// As `PackageStore.listUnverified` over every root; the result is unverified.
    public func listUnverified() throws -> Data {
        try output { ext_layered_list_unverified(handle, $0) }
    }
}

private func check(_ status: ext_status) throws {
    if let error = PackageStoreError(status: status) { throw error }
}

/// Runs `call` with an output buffer, takes its bytes and frees the engine's copy.
private func output(_ call: (UnsafeMutablePointer<ext_buffer>) -> ext_status) throws -> Data {
    var out = ext_buffer(data: nil, len: 0)
    defer { ext_buffer_free(&out) }
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
