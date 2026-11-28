import CryptoKit
import ExtHost
import ExtPluginGuest
import ExtSystem
import Foundation

#if os(macOS)
private struct CaptureRequest: Decodable {
    let target: WindowInfo
    let maxPixels: Int
    let maxBytes: Int
    let timeoutMS: Int

    enum CodingKeys: String, CodingKey {
        case target
        case maxPixels = "max_pixels"
        case maxBytes = "max_bytes"
        case timeoutMS = "timeout_ms"
    }
}

private struct SurfaceInput: Decodable {
    let kind: String
    let point: SurfacePoint?
    let end: SurfacePoint?
    let button: String?
    let deltaX: Int?
    let deltaY: Int?
    let text: String?
    let key: String?
    let modifiers: [String]?

    enum CodingKeys: String, CodingKey {
        case kind, point, end, button, text, key, modifiers
        case deltaX = "delta_x"
        case deltaY = "delta_y"
    }
}

private struct InputRequest: Decodable {
    let target: WindowInfo
    let input: SurfaceInput
    let maxTextRunes: Int
    let timeoutMS: Int

    enum CodingKeys: String, CodingKey {
        case target, input
        case maxTextRunes = "max_text_runes"
        case timeoutMS = "timeout_ms"
    }
}

private struct CaptureResult: Encodable {
    let format: String
    let width: Int
    let height: Int
    let data: Data
    let capturedAt: Date

    enum CodingKeys: String, CodingKey {
        case format, width, height, data
        case capturedAt = "captured_at"
    }
}

// The JSON-line reader runs on a worker. AppKit and ScreenCaptureKit work runs
// on the main actor, whose run loop remains free while the worker waits.
private final class MainResult<T>: @unchecked Sendable {
    let ready = DispatchSemaphore(value: 0)
    var value: Result<T, Error>?
}

private func onMain<T>(_ body: @escaping @MainActor () async throws -> T) throws -> T {
    let result = MainResult<T>()
    Task { @MainActor in
        do { result.value = .success(try await body()) }
        catch { result.value = .failure(error) }
        result.ready.signal()
    }
    result.ready.wait()
    guard let value = result.value else { throw RemoteError(code: "unavailable", message: "surface worker failed") }
    return try value.get()
}

@MainActor private func controller(maxTextRunes: Int = 4_096) throws -> SystemSurfaceController {
    let options = try SurfaceControlOptions(maxTextLength: maxTextRunes)
    return SystemSurfaceController(options: options, processStart: { pid in
        try? LedgerProcessIdentity.startID(pid: pid)
    })
}

private func check(_ target: WindowInfo, timeoutMS: Int) throws {
    guard target.id.hasPrefix("window:"), target.pid > 0, !target.processStartID.isEmpty,
          target.bounds.width > 0, target.bounds.height > 0,
          (1...3_600_000).contains(timeoutMS) else {
        throw RemoteError(code: "invalid", message: "invalid surface target or timeout")
    }
}

private func handle(_ call: Call) throws -> Data {
    guard call.contract.name == "ext.system.surface", call.contract.version == "v1" else {
        throw RemoteError(code: "unsupported", message: "unknown surface contract")
    }
    try call.checkDeadline()
    do {
        switch call.operation {
        case "capture":
            let request = try JSONDecoder().decode(CaptureRequest.self, from: call.payload)
            try check(request.target, timeoutMS: request.timeoutMS)
            guard (1...268_435_456).contains(request.maxPixels), (1...(1 << 30)).contains(request.maxBytes) else {
                throw RemoteError(code: "invalid", message: "invalid capture limits")
            }
            guard #available(macOS 14.0, *) else {
                throw RemoteError(code: "unsupported", message: "window capture requires macOS 14 or later")
            }
            let image = try onMain {
                let native = try controller()
                let options = try SurfaceCaptureOptions(maxPixels: request.maxPixels, maxBytes: request.maxBytes,
                                                        timeout: Double(request.timeoutMS) / 1_000)
                return try await native.capture(window: request.target, options: options)
            }
            let encoder = JSONEncoder()
            encoder.dateEncodingStrategy = .iso8601
            return try encoder.encode(CaptureResult(format: image.format, width: image.width,
                                                    height: image.height, data: image.data,
                                                    capturedAt: Date()))
        case "input":
            let request = try JSONDecoder().decode(InputRequest.self, from: call.payload)
            try check(request.target, timeoutMS: request.timeoutMS)
            guard (1...1_000_000).contains(request.maxTextRunes) else {
                throw RemoteError(code: "invalid", message: "invalid input limit")
            }
            try onMain {
                let native = try controller(maxTextRunes: request.maxTextRunes)
                let input = request.input
                switch input.kind {
                case "pointer-move":
                    guard let point = input.point else { throw SurfaceControlError.invalidInput }
                    try native.move(window: request.target, x: point.x, y: point.y)
                case "pointer-click":
                    guard let point = input.point,
                          let button = SurfacePointerButton(rawValue: input.button ?? "left") else {
                        throw SurfaceControlError.invalidInput
                    }
                    try native.click(window: request.target, x: point.x, y: point.y, button: button)
                case "pointer-drag":
                    guard let point = input.point, let end = input.end else { throw SurfaceControlError.invalidInput }
                    try native.drag(window: request.target, from: point, to: end)
                case "pointer-scroll":
                    guard let point = input.point else { throw SurfaceControlError.invalidInput }
                    try native.scroll(window: request.target, x: point.x, y: point.y,
                                      deltaX: input.deltaX ?? 0, deltaY: input.deltaY ?? 0)
                case "keyboard-text":
                    guard let text = input.text else { throw SurfaceControlError.invalidInput }
                    try native.type(window: request.target, text: text)
                case "keyboard-key":
                    guard let key = input.key else { throw SurfaceControlError.invalidInput }
                    let modifiers = input.modifiers ?? []
                    guard Set(modifiers).count == modifiers.count else { throw SurfaceControlError.invalidInput }
                    try native.press(window: request.target, key: key, modifiers: Set(modifiers))
                default: throw SurfaceControlError.invalidInput
                }
            }
            return Data("{}".utf8)
        default: throw RemoteError(code: "unsupported", message: "unknown surface operation")
        }
    } catch let error as SurfaceControlError {
        let code: String
        switch error {
        case .invalidInput, .invalidLimit: code = "invalid"
        case .permissionDenied: code = "denied"
        case .staleReference: code = "stale"
        case .notFocused: code = "not_focused"
        case .captureTooLarge: code = "limit"
        case .timeout: code = "timeout"
        case .canceled: code = "canceled"
        case .nativeFailure: code = "unavailable"
        }
        throw RemoteError(code: code, message: "surface operation failed")
    } catch is DecodingError {
        throw RemoteError(code: "invalid", message: "invalid surface request")
    }
}

guard CommandLine.arguments.count == 2, CommandLine.arguments[1] == "run" else { exit(2) }
do {
    let binary = URL(fileURLWithPath: CommandLine.arguments[0])
    let revision = SHA256.hash(data: try Data(contentsOf: binary)).map { String(format: "%02x", $0) }.joined()
    let descriptor = Descriptor(identity: Identity(id: "system.surface.darwin", revision: revision, version: "1.0.0"),
        contracts: [Contract(name: "ext.system.surface", version: "v1", operations: [
            Operation(name: "capture", surface: "observation"), Operation(name: "input", surface: "action")
        ])])
    let guest = try Guest(descriptor: descriptor, maxCallMilliseconds: 3_600_000, handler: { call in try handle(call) })
    DispatchQueue.global(qos: .userInitiated).async {
        do { try JSONLine.serveStandardIO(guest); exit(0) }
        catch { FileHandle.standardError.write(Data("surface guest: \(error)\n".utf8)); exit(1) }
    }
    RunLoop.main.run()
} catch {
    FileHandle.standardError.write(Data("surface guest: \(error)\n".utf8))
    exit(1)
}
#else
exit(2)
#endif
