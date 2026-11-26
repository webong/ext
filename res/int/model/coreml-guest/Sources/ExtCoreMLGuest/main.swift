import CryptoKit
import Darwin
import ExtML
import Foundation

// This executable is a macOS guest for model.tensor/v1. Keep the identity in
// sync with the ID and version passed to `ctn model provider package`.
private let pluginID = "coreml"
private let pluginVersion = "1.0.0"
private let apiVersion = "ext.plugin/v1"
private let maxFrameBytes = 24 << 20
private let maxTensorBytes = 16 << 20

private struct InferRequest: Decodable {
    let root: String
    let reference: String
    let inputs: [MLTensor]
}

private struct InferResponse: Encodable {
    let outputs: [MLTensor]
}

private enum GuestError: Error {
    case invalidRequest
    case frameTooLarge
}

private func executableDigest() throws -> String {
    guard let url = Bundle.main.executableURL else { throw GuestError.invalidRequest }
    let file = try FileHandle(forReadingFrom: url)
    defer { try? file.close() }
    var hash = SHA256()
    while let chunk = try file.read(upToCount: 64 << 10), !chunk.isEmpty {
        hash.update(data: chunk)
    }
    return hash.finalize().map { String(format: "%02x", $0) }.joined()
}

private func deadline(_ value: Any?) -> Date? {
    guard let value = value as? String else { return nil }
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let parsed = formatter.date(from: value) { return parsed }
    formatter.formatOptions = [.withInternetDateTime]
    return formatter.date(from: value)
}

private func tensorBytes(_ tensors: [MLTensor]) throws {
    guard !tensors.isEmpty, tensors.count <= 64 else { throw GuestError.invalidRequest }
    var total = 0
    for tensor in tensors {
        try MLTensorValidation.validate(tensor)
        guard tensor.data.count <= maxTensorBytes - total else { throw GuestError.invalidRequest }
        total += tensor.data.count
    }
}

private func modelReference(_ value: String) throws -> MLReference {
    let parts = value.split(separator: "/", omittingEmptySubsequences: false)
    guard parts.count == 2, parts[0] == "coreml" else { throw GuestError.invalidRequest }
    let nameAndVersion = parts[1].split(separator: "@", omittingEmptySubsequences: false)
    guard nameAndVersion.count == 2 else { throw GuestError.invalidRequest }
    let name = String(nameAndVersion[0])
    let version = String(nameAndVersion[1])
    guard MLValidation.isValidName(name, "model name"), MLValidation.isValidVersion(version) else {
        throw GuestError.invalidRequest
    }
    return MLReference(provider: "coreml", name: name, version: version)
}

private func infer(_ payload: Any?) throws -> [String: Any] {
    guard let payload = payload as? [String: Any] else { throw GuestError.invalidRequest }
    let data = try JSONSerialization.data(withJSONObject: payload)
    let request = try JSONDecoder().decode(InferRequest.self, from: data)
    guard request.root.hasPrefix("/"), request.root.utf8.count <= 4096 else {
        throw GuestError.invalidRequest
    }
    try tensorBytes(request.inputs)
    let reference = try modelReference(request.reference)
    let store = try MLStore(rootURL: URL(fileURLWithPath: request.root))
    let model = try store.resolve(reference: reference)
    let outputs = try MLInference.run(provider: CoreMLProvider(), model: model, inputs: request.inputs)
    try tensorBytes(outputs)
    let encoded = try JSONEncoder().encode(InferResponse(outputs: outputs))
    guard let object = try JSONSerialization.jsonObject(with: encoded) as? [String: Any] else {
        throw GuestError.invalidRequest
    }
    return object
}

private final class Guest {
    private let identity: [String: String]
    private var handshaken = false

    init() throws {
        identity = ["id": pluginID, "revision": try executableDigest(), "version": pluginVersion]
    }

    private var descriptor: [String: Any] {
        ["apiVersion": apiVersion, "identity": identity,
         "contracts": [["name": "model.tensor", "version": "v1",
                        "operations": [["name": "infer", "surface": "action"]]]]]
    }

    private func respond(_ id: String, payload: [String: Any]? = nil,
                         code: String? = nil, message: String? = nil) throws {
        var response: [String: Any] = ["apiVersion": apiVersion, "id": id]
        if let payload = payload {
            response["payload"] = payload
        } else {
            response["error"] = ["code": code ?? "native_failure",
                                 "message": message ?? "model inference failed"]
        }
        var data = try JSONSerialization.data(withJSONObject: response, options: [.sortedKeys])
        guard data.count <= maxFrameBytes else { throw GuestError.frameTooLarge }
        data.append(0x0a)
        try FileHandle.standardOutput.write(contentsOf: data)
    }

    private func handle(_ line: Data) throws {
        guard line.count <= maxFrameBytes,
              let request = try JSONSerialization.jsonObject(with: line) as? [String: Any],
              request["apiVersion"] as? String == apiVersion,
              let id = request["id"] as? String,
              let operation = request["operation"] as? String,
              let due = deadline(request["deadline"]), due > Date() else {
            throw GuestError.invalidRequest
        }
        if !handshaken {
            guard id == "hello", operation == "plugin.hello" else { throw GuestError.invalidRequest }
            try respond(id, payload: descriptor)
            handshaken = true
            return
        }
        guard operation == "infer", request["surface"] as? String == "action",
              let plugin = request["plugin"] as? [String: String], plugin == identity,
              let contract = request["contract"] as? [String: String],
              contract == ["name": "model.tensor", "version": "v1"] else {
            try respond(id, code: "invalid_input", message: "request does not match model.tensor/v1")
            return
        }
        do {
            try respond(id, payload: infer(request["payload"]))
        } catch is GuestError {
            try respond(id, code: "invalid_input", message: "invalid tensor inference request")
        } catch is DecodingError {
            try respond(id, code: "invalid_input", message: "invalid tensor inference request")
        } catch {
            try respond(id, code: "native_failure", message: "CoreML model inference failed")
        }
    }

    func serve() throws {
        var pending = Data()
        var buffer = [UInt8](repeating: 0, count: 64 << 10)
        while true {
            let count = buffer.withUnsafeMutableBytes { bytes in
                Darwin.read(STDIN_FILENO, bytes.baseAddress, bytes.count)
            }
            if count < 0 { throw GuestError.invalidRequest }
            if count == 0 { break }
            pending.append(contentsOf: buffer.prefix(count))
            while let newline = pending.firstIndex(of: 0x0a) {
                let frame = pending.prefix(upTo: newline)
                pending.removeSubrange(...newline)
                try handle(Data(frame))
            }
            guard pending.count <= maxFrameBytes else { throw GuestError.frameTooLarge }
        }
        guard pending.isEmpty else { throw GuestError.invalidRequest }
    }
}

do {
    guard CommandLine.arguments.count == 2, CommandLine.arguments[1] == "run" else {
        throw GuestError.invalidRequest
    }
    try Guest().serve()
} catch {
    fputs("ext-coreml-guest: \(error)\n", stderr)
    exit(1)
}
