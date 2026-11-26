import CryptoKit
import Darwin
import ExtML
import ExtPluginGuest
import Foundation

// Keep this identity in sync with `ext ctn model provider package`.
private let pluginID = "coreml"
private let pluginVersion = "1.0.0"
private let maxTensorBytes = 16 << 20

private struct InferRequest: Decodable {
    let root: String
    let reference: String
    let inputs: [MLTensor]
}

private struct InferResponse: Encodable {
    let outputs: [MLTensor]
}

private enum RequestError: Error {
    case invalid
}

private func executableDigest() throws -> String {
    guard let url = Bundle.main.executableURL else { throw RequestError.invalid }
    let file = try FileHandle(forReadingFrom: url)
    defer { try? file.close() }
    var hash = SHA256()
    while let chunk = try file.read(upToCount: 64 << 10), !chunk.isEmpty {
        hash.update(data: chunk)
    }
    return hash.finalize().map { String(format: "%02x", $0) }.joined()
}

private func tensorBytes(_ tensors: [MLTensor]) throws {
    guard !tensors.isEmpty, tensors.count <= 64 else { throw RequestError.invalid }
    var total = 0
    for tensor in tensors {
        try MLTensorValidation.validate(tensor)
        guard tensor.data.count <= maxTensorBytes - total else { throw RequestError.invalid }
        total += tensor.data.count
    }
}

private func modelReference(_ value: String) throws -> MLReference {
    let parts = value.split(separator: "/", omittingEmptySubsequences: false)
    guard parts.count == 2, parts[0] == "coreml" else { throw RequestError.invalid }
    let nameAndVersion = parts[1].split(separator: "@", omittingEmptySubsequences: false)
    guard nameAndVersion.count == 2 else { throw RequestError.invalid }
    let name = String(nameAndVersion[0])
    let version = String(nameAndVersion[1])
    guard MLValidation.isValidName(name, "model name"), MLValidation.isValidVersion(version) else {
        throw RequestError.invalid
    }
    return MLReference(provider: pluginID, name: name, version: version)
}

private func handle(_ call: Call) throws -> Data {
    guard call.contract == ContractRef(name: "model.tensor", version: "v1"),
          call.operation == "infer", call.surface == "action" else {
        throw RemoteError(code: "invalid_input", message: "request does not match model.tensor/v1")
    }
    try call.checkDeadline()

    let request: InferRequest
    let reference: MLReference
    do {
        request = try call.decodePayload(InferRequest.self)
        guard request.root.hasPrefix("/"), request.root.utf8.count <= 4096 else {
            throw RequestError.invalid
        }
        try tensorBytes(request.inputs)
        reference = try modelReference(request.reference)
    } catch {
        throw RemoteError(code: "invalid_input", message: "invalid tensor inference request")
    }

    do {
        let store = try MLStore(rootURL: URL(fileURLWithPath: request.root))
        let model = try store.resolve(reference: reference)
        let outputs = try MLInference.run(provider: CoreMLProvider(), model: model, inputs: request.inputs)
        try call.checkDeadline()
        try tensorBytes(outputs)
        return try JSONEncoder().encode(InferResponse(outputs: outputs))
    } catch {
        throw RemoteError(code: "native_failure", message: "CoreML model inference failed")
    }
}

guard CommandLine.arguments.count == 2, CommandLine.arguments[1] == "run" else { exit(2) }
do {
    let descriptor = Descriptor(
        identity: Identity(id: pluginID, revision: try executableDigest(), version: pluginVersion),
        contracts: [Contract(name: "model.tensor", version: "v1",
                             operations: [Operation(name: "infer", surface: "action")])]
    )
    let guest = try Guest(descriptor: descriptor, handler: { call in try handle(call) })
    try JSONLine.serveStandardIO(guest)
} catch {
    fputs("ext-coreml-guest: \(error)\n", stderr)
    exit(1)
}
