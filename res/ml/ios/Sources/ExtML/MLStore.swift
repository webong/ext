import CryptoKit
import Foundation

/// Bounded, caller-selected model artifact store. Bundles are stored under
/// `<root>/<provider>/<name>/<version>/` with a `manifest.json` and artifact
/// files. Installation stages, verifies SHA-256 digests, and atomically
/// publishes only after all checks pass.
public final class MLStore {
    private let rootURL: URL
    private let queue = DispatchQueue(label: "io.github.webong.ext.ml.store", attributes: .concurrent)

    /// Maximum total bundle size in bytes.
    public let maxTotalBytes: Int64
    /// Maximum artifact file size in bytes.
    public let maxArtifactBytes: Int64
    /// Maximum number of artifacts in one bundle.
    public let maxArtifacts: Int

    public init(rootURL: URL,
                maxTotalBytes: Int64 = 1 << 40,
                maxArtifactBytes: Int64 = 64 << 30,
                maxArtifacts: Int = 64) throws {
        guard rootURL.isFileURL else {
            throw MLError.invalidValue
        }
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: rootURL.path, isDirectory: &isDirectory),
              isDirectory.boolValue else {
            throw MLError.invalidValue
        }
        self.rootURL = rootURL
        self.maxTotalBytes = maxTotalBytes
        self.maxArtifactBytes = maxArtifactBytes
        self.maxArtifacts = maxArtifacts
    }

    public var rootPath: String { rootURL.path }

    /// Installs one model bundle from streaming artifact sources. Never
    /// replaces an existing bundle: an identical manifest is returned as
    /// already installed; a different one throws `MLError.alreadyExists`.
    ///
    /// Each artifact is streamed through a SHA-256 hash and size check as it
    /// is copied into the staging directory, so large bundles never need to be
    /// held entirely in memory. If any check fails, the staging directory is
    /// removed and no partial bundle is visible at the target path.
    public func install(manifest: MLManifest, files: [String: ArtifactSource]) throws -> MLInstalledModel {
        try manifest.validate()
        guard files.count == manifest.artifacts.count else {
            throw MLError.invalidValue
        }
        let reference = MLReference(provider: manifest.provider, name: manifest.name,
                                    version: manifest.version)
        let targetURL = rootURL.appendingPathComponent(reference.provider)
            .appendingPathComponent(reference.name)
            .appendingPathComponent(reference.version)

        // Detect an existing bundle.
        if let existing = try? resolve(reference: reference),
           existing.manifest == manifest {
            return existing
        }
        if FileManager.default.fileExists(atPath: targetURL.path) {
            throw MLError.alreadyExists
        }

        // Stage into a temporary sibling directory.
        let temporary = rootURL.appendingPathComponent(".ml-install-\(UUID().uuidString)")
        try? FileManager.default.removeItem(at: temporary)
        try FileManager.default.createDirectory(at: temporary, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: temporary) }

        var total: Int64 = 0
        for artifact in manifest.artifacts {
            guard let source = files[artifact.file] else {
                throw MLError.invalidValue
            }
            guard source.size() < 0 || Int64(source.size()) <= maxArtifactBytes else {
                throw MLError.invalidValue
            }
            let stream = try source.open()
            stream.open()
            defer { stream.close() }

            let artifactURL = temporary.appendingPathComponent(artifact.file)
            guard let output = OutputStream(url: artifactURL, append: false) else {
                throw MLError.invalidValue
            }
            output.open()
            defer { output.close() }

            var hasher = CryptoKit.SHA256()
            var artifactBytes: Int64 = 0
            let bufferSize = 64 * 1024
            let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
            defer { buffer.deallocate() }

            while true {
                let read = stream.read(buffer, maxLength: bufferSize)
                if read < 0 {
                    throw MLError.nativeFailure
                }
                if read == 0 {
                    break
                }
                let slice = UnsafeBufferPointer(start: buffer, count: read)
                hasher.update(bufferPointer: UnsafeRawBufferPointer(slice))
                artifactBytes += Int64(read)
                if artifactBytes > maxArtifactBytes {
                    throw MLError.invalidValue
                }
                total += Int64(read)
                if total > maxTotalBytes {
                    throw MLError.invalidValue
                }
                _ = output.write(buffer, maxLength: read)
            }

            let digest = hasher.finalize().compactMap { String(format: "%02x", $0) }.joined()
            guard digest == artifact.sha256 else {
                throw MLError.digestMismatch
            }
        }

        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let manifestData = try encoder.encode(manifest)
        try manifestData.write(to: temporary.appendingPathComponent("manifest.json"))

        // Create parent directories and atomically move into place.
        let targetParent = targetURL.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: targetParent, withIntermediateDirectories: true)
        try FileManager.default.moveItem(at: temporary, to: targetURL)

        return MLInstalledModel(manifest: manifest, directory: targetURL.path)
    }

    /// Convenience wrapper for small artifacts already in memory. Delegates to
    /// `install(manifest:files:)` by wrapping each `Data` value in an
    /// `InMemoryArtifactSource`.
    public func installFromBytes(manifest: MLManifest, files: [String: Data]) throws -> MLInstalledModel {
        var sources: [String: ArtifactSource] = [:]
        for (name, data) in files {
            sources[name] = InMemoryArtifactSource(data)
        }
        return try install(manifest: manifest, files: sources)
    }

    /// Loads one installed model and re-verifies every artifact digest.
    public func resolve(reference: MLReference) throws -> MLInstalledModel {
        let relativePath = "\(reference.provider)/\(reference.name)/\(reference.version)"
        let targetURL = rootURL.appendingPathComponent(relativePath)
        let manifestURL = targetURL.appendingPathComponent("manifest.json")
        guard let data = try? Data(contentsOf: manifestURL) else {
            throw MLError.notFound
        }
        guard data.count <= mlMaxManifestBytes else {
            throw MLError.invalidValue
        }
        let decoder = JSONDecoder()
        guard let manifest = try? decoder.decode(MLManifest.self, from: data) else {
            throw MLError.invalidValue
        }
        try manifest.validate()
        guard manifest.provider == reference.provider,
              manifest.name == reference.name,
              manifest.version == reference.version else {
            throw MLError.invalidValue
        }
        for artifact in manifest.artifacts {
            let artifactURL = targetURL.appendingPathComponent(artifact.file)
            guard let artifactData = try? Data(contentsOf: artifactURL) else {
                throw MLError.notFound
            }
            guard MLDigest.sha256(artifactData) == artifact.sha256 else {
                throw MLError.digestMismatch
            }
        }
        return MLInstalledModel(manifest: manifest, directory: targetURL.path)
    }

    /// Lists every installed model with digests re-verified.
    public func list() throws -> [MLInstalledModel] {
        var result: [MLInstalledModel] = []
        let providerURLs = (try? FileManager.default.contentsOfDirectory(at: rootURL,
                                                                           includingPropertiesForKeys: nil)) ?? []
        for providerURL in providerURLs where providerURL.hasDirectoryPath {
            let nameURLs = (try? FileManager.default.contentsOfDirectory(at: providerURL,
                                                                         includingPropertiesForKeys: nil)) ?? []
            for nameURL in nameURLs where nameURL.hasDirectoryPath {
                let versionURLs = (try? FileManager.default.contentsOfDirectory(at: nameURL,
                                                                                includingPropertiesForKeys: nil)) ?? []
                for versionURL in versionURLs where versionURL.hasDirectoryPath {
                    let reference = MLReference(provider: providerURL.lastPathComponent,
                                                name: nameURL.lastPathComponent,
                                                version: versionURL.lastPathComponent)
                    if let installed = try? resolve(reference: reference) {
                        result.append(installed)
                    }
                }
            }
        }
        result.sort { $0.manifest.stringValue < $1.manifest.stringValue }
        return result
    }

    /// Deletes one installed model bundle and prunes empty parent directories.
    public func remove(reference: MLReference) throws {
        let relativePath = "\(reference.provider)/\(reference.name)/\(reference.version)"
        let targetURL = rootURL.appendingPathComponent(relativePath)
        guard FileManager.default.fileExists(atPath: targetURL.path) else {
            return
        }
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: targetURL.path, isDirectory: &isDirectory),
              isDirectory.boolValue else {
            throw MLError.invalidValue
        }
        try FileManager.default.removeItem(at: targetURL)
        // Prune empty name and provider directories.
        let nameURL = targetURL.deletingLastPathComponent()
        let providerURL = nameURL.deletingLastPathComponent()
        for dirURL in [nameURL, providerURL] where dirURL.path != rootURL.path {
            let entries = (try? FileManager.default.contentsOfDirectory(at: dirURL,
                                                                        includingPropertiesForKeys: nil)) ?? []
            if entries.isEmpty {
                try? FileManager.default.removeItem(at: dirURL)
            }
        }
    }
}

public extension MLManifest {
    var stringValue: String {
        return "\(provider)/\(name)@\(version)"
    }

    func validate() throws {
        guard apiVersion == mlManifestAPIVersion else {
            throw MLError.invalidValue
        }
        guard MLValidation.isValidName(name, "model name") else {
            throw MLError.invalidValue
        }
        guard MLValidation.isValidVersion(version) else {
            throw MLError.invalidValue
        }
        guard MLValidation.isValidName(provider, "provider") else {
            throw MLError.invalidValue
        }
        guard capabilities.count <= mlMaxCapabilities else {
            throw MLError.invalidValue
        }
        var seenCapabilities = Set<String>()
        for capability in capabilities {
            guard MLValidation.isValidName(capability, "capability") else {
                throw MLError.invalidValue
            }
            guard seenCapabilities.insert(capability).inserted else {
                throw MLError.invalidValue
            }
        }
        guard artifacts.count >= 1, artifacts.count <= mlMaxArtifacts else {
            throw MLError.invalidValue
        }
        var seenFiles = Set<String>()
        var seenRoles = Set<String>()
        for artifact in artifacts {
            try artifact.validate()
            guard seenFiles.insert(artifact.file).inserted else {
                throw MLError.invalidValue
            }
            guard seenRoles.insert(artifact.role).inserted else {
                throw MLError.invalidValue
            }
        }
    }
}

public extension MLArtifact {
    func validate() throws {
        guard MLValidation.isValidName(role, "artifact role") else {
            throw MLError.invalidValue
        }
        guard MLValidation.isValidName(file, "artifact file") else {
            throw MLError.invalidValue
        }
        guard MLValidation.isHexSHA256(sha256) else {
            throw MLError.invalidValue
        }
    }
}
