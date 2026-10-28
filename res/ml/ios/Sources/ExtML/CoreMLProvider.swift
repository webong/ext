import CoreML
import Foundation

/// CoreML-backed model provider. Loads compiled CoreML models (`.mlmodelc`)
/// from a verified model bundle directory and runs inference through
/// `MLModel.prediction(from:options:)`.
///
/// Input and output feature names default to `"input"` and `"output"`.
/// Tensors must be `float32` and match the model's expected multi-array shape.
public final class CoreMLProvider: MLProvider {
    public let name = "coreml"
    public let capabilities: [String]

    public init(capabilities: [String] = []) {
        self.capabilities = capabilities
    }

    public func load(model: MLInstalledModel) throws -> MLSession {
        let modelURL = URL(fileURLWithPath: model.directory)
        // Prefer a compiled model directory; fall back to an uncompiled model file.
        let compiled = modelURL.appendingPathComponent("model.mlmodelc")
        let uncompiled = modelURL.appendingPathComponent("model.mlmodel")
        let resolvedURL: URL
        if FileManager.default.fileExists(atPath: compiled.path) {
            resolvedURL = compiled
        } else if FileManager.default.fileExists(atPath: uncompiled.path) {
            resolvedURL = uncompiled
        } else {
            throw MLError.notFound
        }
        let model = try MLModel(contentsOf: resolvedURL, configuration: MLModelConfiguration())
        return CoreMLSession(model: model)
    }
}

/// A CoreML inference session.
public final class CoreMLSession: MLSession {
    private let model: MLModel

    fileprivate init(model: MLModel) {
        self.model = model
    }

    public func infer(inputs: [MLTensor]) throws -> [MLTensor] {
        guard let tensor = inputs.first else {
            throw MLError.invalidValue
        }
        try MLTensorValidation.validate(tensor)
        let multiArray = try MLMultiArray(shape: tensor.shape.map { NSNumber(value: $0) },
                                          dataType: MLMultiArrayDataType.float32)
        let count = tensor.shape.reduce(1, *)
        let values = tensor.float32Values()
        for index in 0..<count {
            multiArray[index] = NSNumber(value: values[index])
        }
        let input = try MLDictionaryFeatureProvider(dictionary: ["input": MLFeatureValue(multiArray: multiArray)])
        let options = MLPredictionOptions()
        let output = try model.prediction(from: input, options: options)
        guard let feature = output.featureValue(for: "output"),
              let multiArrayOutput = feature.multiArrayValue,
              multiArrayOutput.dataType == MLMultiArrayDataType.float32 else {
            throw MLError.nativeFailure
        }
        let outputShape = multiArrayOutput.shape.map { $0.intValue }
        let outputCount = outputShape.reduce(1, *)
        var data = Data(count: outputCount * 4)
        data.withUnsafeMutableBytes { buffer in
            let floatBuffer = buffer.bindMemory(to: Float.self)
            for index in 0..<outputCount {
                floatBuffer[index] = Float(truncating: multiArrayOutput[index])
            }
        }
        return [MLTensor(shape: outputShape, data: data, format: "float32")]
    }

    public func close() throws {
        // CoreML models release their resources on deinit.
    }
}
