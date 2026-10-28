import CryptoKit
import Foundation

/// The declared version of the manifest contract.
public let mlManifestAPIVersion = "content.model/v1"

/// Maximum manifest size in bytes.
public let mlMaxManifestBytes = 64 * 1024
/// Maximum number of artifacts in one model bundle.
public let mlMaxArtifacts = 64
/// Maximum artifact file size in bytes.
public let mlMaxArtifactBytes = 64 * 1024 * 1024
/// Maximum name length for model names, providers, roles, and file names.
public let mlMaxNameLength = 128
/// Maximum number of declared capabilities.
public let mlMaxCapabilities = 32

/// One artifact in a model bundle.
public struct MLArtifact: Codable, Equatable, Sendable {
    public let role: String
    public let file: String
    public let sha256: String

    public init(role: String, file: String, sha256: String) {
        self.role = role
        self.file = file
        self.sha256 = sha256
    }
}

/// One model bundle manifest.
public struct MLManifest: Codable, Equatable, Sendable {
    public let apiVersion: String
    public let name: String
    public let version: String
    public let provider: String
    public let capabilities: [String]
    public let artifacts: [MLArtifact]

    public init(apiVersion: String, name: String, version: String,
                provider: String, capabilities: [String], artifacts: [MLArtifact]) {
        self.apiVersion = apiVersion
        self.name = name
        self.version = version
        self.provider = provider
        self.capabilities = capabilities
        self.artifacts = artifacts
    }
}

/// An inspected, verified model bundle.
public struct MLInstalledModel: Codable, Equatable, Sendable {
    public let manifest: MLManifest
    public let directory: String

    public init(manifest: MLManifest, directory: String) {
        self.manifest = manifest
        self.directory = directory
    }
}

/// One multi-dimensional tensor for inference.
public struct MLTensor: Codable, Equatable, Sendable {
    public let shape: [Int]
    public let data: Data
    public let format: String

    public init(shape: [Int], data: Data, format: String) {
        self.shape = shape
        self.data = data
        self.format = format
    }

    /// Returns the tensor data as little-endian float32 values.
    public func float32Values() -> [Float] {
        guard data.count % 4 == 0 else { return [] }
        var values = [Float](repeating: 0, count: data.count / 4)
        data.withUnsafeBytes { buffer in
            let floatBuffer = buffer.bindMemory(to: Float.self)
            for index in 0..<values.count {
                values[index] = floatBuffer[index]
            }
        }
        return values
    }
}

/// One model reference: provider/name@version.
public struct MLReference: Codable, Equatable, Sendable {
    public let provider: String
    public let name: String
    public let version: String

    public init(provider: String, name: String, version: String) {
        self.provider = provider
        self.name = name
        self.version = version
    }

    public var stringValue: String {
        return "\(provider)/\(name)@\(version)"
    }
}

/// Errors thrown by the ML store and providers.
public enum MLError: Error, Equatable {
    case invalidValue
    case notFound
    case alreadyExists
    case digestMismatch
    case unavailable
    case notLoaded
    case nativeFailure
}

/// Validates a manifest name (model, provider, capability, role, file).
public enum MLValidation {
    public static func isValidName(_ value: String, _ what: String) -> Bool {
        guard !value.isEmpty, value.count <= mlMaxNameLength,
              value.allSatisfy({ $0 >= " " && $0 <= "~" && $0 != "/" && $0 != "\\" }) else {
            return false
        }
        guard let first = value.first else { return false }
        guard first != "." && first != "-" && first != "_" else { return false }
        return value.trimmingCharacters(in: .whitespacesAndNewlines) == value
    }

    public static func isValidVersion(_ value: String) -> Bool {
        guard !value.isEmpty, value.count <= mlMaxNameLength,
              value.allSatisfy({ $0 >= " " && $0 <= "~" && $0 != "/" && $0 != "\\" }) else {
            return false
        }
        return value.trimmingCharacters(in: .whitespacesAndNewlines) == value
    }

    public static func isHexSHA256(_ value: String) -> Bool {
        guard value.count == 64 else { return false }
        return value.allSatisfy {
            ($0 >= "0" && $0 <= "9") || ($0 >= "a" && $0 <= "f")
        }
    }
}

/// SHA-256 digest helper.
public enum MLDigest {
    public static func sha256(_ data: Data) -> String {
        let digest = CryptoKit.SHA256.hash(data: data)
        return digest.map { String(format: "%02x", $0) }.joined()
    }
}

/// A bounded, readable source for one model artifact. The primary installation
/// API accepts streaming sources so that large artifacts can be hashed and
/// staged without ever being held entirely in memory.
public protocol ArtifactSource: Sendable {
    /// Opens a readable stream for the artifact. The caller must close the
    /// returned stream. Open is called once per artifact.
    func open() throws -> InputStream
    /// Returns the declared byte size of the artifact, or -1 if unknown.
    func size() -> Int64
}

/// An in-memory artifact source wrapping a Data value. A convenience wrapper
/// for small artifacts; large artifacts should use a streaming source that
/// reads from disk or network.
public struct InMemoryArtifactSource: ArtifactSource {
    private let data: Data

    public init(_ data: Data) {
        self.data = data
    }

    public func open() throws -> InputStream {
        return InputStream(data: data)
    }

    public func size() -> Int64 {
        return Int64(data.count)
    }
}
