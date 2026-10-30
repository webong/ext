import Foundation

/// A caller-selected native model inference backend.
public protocol MLProvider: AnyObject {
    /// The provider identity (for example "coreml", "nnapi", "onnxruntime").
    var name: String { get }
    /// The model kinds this provider can load.
    var capabilities: [String] { get }
    /// Binds an installed model to a new session.
    func load(model: MLInstalledModel) throws -> MLSession
}

/// One inference session bound to a loaded model.
public protocol MLSession: AnyObject {
    /// Runs one inference pass over the input tensors.
    func infer(inputs: [MLTensor]) throws -> [MLTensor]
    /// Releases the model and any native resources.
    func close() throws
}

/// Bounded inference through a caller-selected provider.
public enum MLInference {
    public static func run(provider: MLProvider, model: MLInstalledModel,
                           inputs: [MLTensor]) throws -> [MLTensor] {
        let session = try provider.load(model: model)
        defer { try? session.close() }
        return try session.infer(inputs: inputs)
    }
}

/// Tensor validation helpers.
public enum MLTensorValidation {
    public static func validate(_ tensor: MLTensor) throws {
        guard tensor.format == "float32" else {
            throw MLError.invalidValue
        }
        guard tensor.shape.count >= 1, tensor.shape.count <= 8 else {
            throw MLError.invalidValue
        }
        var elements = 1
        for dim in tensor.shape {
            guard dim >= 1, dim <= (1 << 30) else {
                throw MLError.invalidValue
            }
            guard elements <= (1 << 30) / dim else {
                throw MLError.invalidValue
            }
            elements *= dim
        }
        guard tensor.data.count == elements * 4 else {
            throw MLError.invalidValue
        }
    }
}
