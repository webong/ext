import CoreML
import Foundation

/// CoreML-backed model provider. Loads compiled CoreML models (`.mlmodelc`)
/// from a verified model bundle directory and runs inference through
/// `MLModel.prediction(from:options:)`.
///
/// Tensors are ordered by sorted model feature name, use float32, and must
/// match the model's expected multi-array shapes.
public final class CoreMLProvider: MLProvider {
    public let name = "coreml"
    public let capabilities: [String]

    public init(capabilities: [String] = []) {
        self.capabilities = capabilities
    }

    public func load(model: MLInstalledModel) throws -> MLSession {
        try model.manifest.validate()
        guard let artifact = model.manifest.artifacts.first(where: { $0.role == "model" }),
              artifact.file.hasSuffix(".mlmodel"),
              MLValidation.isValidName(artifact.file, "file") else { throw MLError.invalidValue }
        let source = URL(fileURLWithPath: model.directory).appendingPathComponent(artifact.file)
        let values = try source.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey, .fileSizeKey])
        guard values.isRegularFile == true, values.isSymbolicLink != true,
              let fileSize = values.fileSize, fileSize > 0, fileSize <= mlMaxArtifactBytes else { throw MLError.invalidValue }
        let data = try Data(contentsOf: source, options: .mappedIfSafe)
        guard MLDigest.sha256(data) == artifact.sha256 else { throw MLError.digestMismatch }
        let compiled = try MLModel.compileModel(at: source)
        do {
            let loaded = try MLModel(contentsOf: compiled, configuration: MLModelConfiguration())
            return CoreMLSession(model: loaded, compiledURL: compiled)
        } catch {
            try? FileManager.default.removeItem(at: compiled)
            throw error
        }
    }
}

/// A CoreML inference session.
public final class CoreMLSession: MLSession {
    private let model: MLModel
    private let compiledURL: URL
    private var isClosed = false

    fileprivate init(model: MLModel, compiledURL: URL) {
        self.model = model
        self.compiledURL = compiledURL
    }

    public func infer(inputs: [MLTensor]) throws -> [MLTensor] {
        guard !isClosed else { throw MLError.notLoaded }
        let inputNames = model.modelDescription.inputDescriptionsByName.keys.sorted()
        let outputNames = model.modelDescription.outputDescriptionsByName.keys.sorted()
        guard inputs.count == inputNames.count, !outputNames.isEmpty else { throw MLError.invalidValue }
        var features: [String: MLFeatureValue] = [:]
        for (name, tensor) in zip(inputNames, inputs) {
            try MLTensorValidation.validate(tensor)
            guard tensor.data.count <= mlMaxTensorBytes else { throw MLError.invalidValue }
            guard model.modelDescription.inputDescriptionsByName[name]?.type == .multiArray else { throw MLError.invalidValue }
            let multiArray = try MLMultiArray(shape: tensor.shape.map { NSNumber(value: $0) }, dataType: .float32)
            let values = tensor.float32Values()
            for index in values.indices { multiArray[index] = NSNumber(value: values[index]) }
            features[name] = MLFeatureValue(multiArray: multiArray)
        }
        let input = try MLDictionaryFeatureProvider(dictionary: features)
        let options = MLPredictionOptions()
        let output = try model.prediction(from: input, options: options)
        return try outputNames.map { name in
            guard let array = output.featureValue(for: name)?.multiArrayValue,
                  array.dataType == .float32 else { throw MLError.nativeFailure }
            let shape = array.shape.map { $0.intValue }
            guard array.count <= mlMaxTensorBytes / 4 else { throw MLError.invalidValue }
            let count = shape.reduce(1, *)
            var data = Data(count: count * 4)
            data.withUnsafeMutableBytes { buffer in
                let bytes = buffer.bindMemory(to: UInt8.self)
                for index in 0..<count {
                    let bits = Float(truncating: array[index]).bitPattern
                    bytes[index * 4] = UInt8(truncatingIfNeeded: bits)
                    bytes[index * 4 + 1] = UInt8(truncatingIfNeeded: bits >> 8)
                    bytes[index * 4 + 2] = UInt8(truncatingIfNeeded: bits >> 16)
                    bytes[index * 4 + 3] = UInt8(truncatingIfNeeded: bits >> 24)
                }
            }
            return MLTensor(shape: shape, data: data, format: "float32")
        }
    }

    public func close() throws {
        guard !isClosed else { return }
        isClosed = true
        try FileManager.default.removeItem(at: compiledURL)
    }

    deinit { try? FileManager.default.removeItem(at: compiledURL) }
}
