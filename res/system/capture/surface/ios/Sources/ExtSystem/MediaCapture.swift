import AVFoundation
import CoreMedia
import Foundation

public enum SystemMediaCaptureError: Error, Equatable {
    case invalidConfiguration
    case alreadyRunning
    case displayUnavailable
    case permissionDenied
    case nativeFailure
}

#if os(macOS)
import CoreGraphics
import ScreenCaptureKit

public struct MediaCaptureOptions: Equatable, Sendable {
    public let systemAudio: Bool
    public let framesPerSecond: Int

    public init(systemAudio: Bool = false, framesPerSecond: Int = 30) throws {
        guard (1...60).contains(framesPerSecond) else {
            throw SystemMediaCaptureError.invalidConfiguration
        }
        self.systemAudio = systemAudio
        self.framesPerSecond = framesPerSecond
    }
}

/// Samples are borrowed native buffers valid for the duration guaranteed by
/// their Apple framework. Consumers that retain samples must copy them.
public protocol SystemMediaSink: AnyObject {
    func screenSample(_ sample: CMSampleBuffer)
    func systemAudioSample(_ sample: CMSampleBuffer)
    func microphoneSample(_ buffer: AVAudioPCMBuffer, at time: AVAudioTime)
    func mediaStopped(error: Error?)
}

/// Local ScreenCaptureKit and microphone capture. The caller selects an
/// observed display and owns permission prompting, encoding, transport, and
/// session policy.
@available(macOS 13.0, *)
public final class SystemMediaCapture: NSObject, SCStreamOutput, SCStreamDelegate, @unchecked Sendable {
    public weak var sink: SystemMediaSink?

    private var stream: SCStream?
    private var starting = false
    private var generation: UInt = 0
    private var microphone: AVAudioEngine?
    private let queue = DispatchQueue(label: "io.github.webong.ext.system.capture")

    public init(sink: SystemMediaSink) {
        self.sink = sink
    }

    @MainActor
    public func start(display: DisplayInfo, options: MediaCaptureOptions = try! MediaCaptureOptions()) async throws {
        guard stream == nil, !starting else { throw SystemMediaCaptureError.alreadyRunning }
        guard display.bounds.width > 0, display.bounds.height > 0 else {
            throw SystemMediaCaptureError.invalidConfiguration
        }
        guard CGPreflightScreenCaptureAccess() else {
            throw SystemMediaCaptureError.permissionDenied
        }
        starting = true
        generation &+= 1
        let currentGeneration = generation
        defer { starting = false }

        let content: SCShareableContent
        do {
            content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
        } catch {
            throw SystemMediaCaptureError.nativeFailure
        }
        guard currentGeneration == generation else { throw CancellationError() }
        guard let nativeDisplay = content.displays.first(where: {
            opaqueSurfaceID("display", String($0.displayID)) == display.id
        }) else {
            throw SystemMediaCaptureError.displayUnavailable
        }
        let config = SCStreamConfiguration()
        config.width = nativeDisplay.width
        config.height = nativeDisplay.height
        config.minimumFrameInterval = CMTime(value: 1, timescale: CMTimeScale(options.framesPerSecond))
        config.queueDepth = 3
        config.capturesAudio = options.systemAudio
        config.excludesCurrentProcessAudio = true
        config.sampleRate = 48_000
        config.channelCount = 2
        let capture = SCStream(
            filter: SCContentFilter(display: nativeDisplay, excludingWindows: []),
            configuration: config,
            delegate: self
        )
        do {
            try capture.addStreamOutput(self, type: .screen, sampleHandlerQueue: queue)
            if options.systemAudio {
                try capture.addStreamOutput(self, type: .audio, sampleHandlerQueue: queue)
            }
            stream = capture
            try await capture.startCapture()
            if currentGeneration != generation {
                try await capture.stopCapture()
                throw CancellationError()
            }
        } catch let error as SystemMediaCaptureError {
            if stream === capture { stream = nil }
            throw error
        } catch {
            if stream === capture { stream = nil }
            throw SystemMediaCaptureError.nativeFailure
        }
    }

    @MainActor
    public func startMicrophone() throws {
        guard microphone == nil else { throw SystemMediaCaptureError.alreadyRunning }
        guard AVCaptureDevice.authorizationStatus(for: .audio) == .authorized else {
            throw SystemMediaCaptureError.permissionDenied
        }
        let engine = AVAudioEngine()
        let input = engine.inputNode
        let format = input.outputFormat(forBus: 0)
        guard format.sampleRate > 0, format.channelCount > 0 else {
            throw SystemMediaCaptureError.nativeFailure
        }
        input.installTap(onBus: 0, bufferSize: 960, format: format) { [weak self] buffer, time in
            self?.sink?.microphoneSample(buffer, at: time)
        }
        do {
            try engine.start()
            microphone = engine
        } catch {
            input.removeTap(onBus: 0)
            throw SystemMediaCaptureError.nativeFailure
        }
    }

    @MainActor
    public func stop() async throws {
        generation &+= 1
        microphone?.inputNode.removeTap(onBus: 0)
        microphone?.stop()
        microphone = nil
        let capture = stream
        stream = nil
        do {
            try await capture?.stopCapture()
            sink?.mediaStopped(error: nil)
        } catch {
            sink?.mediaStopped(error: error)
            throw SystemMediaCaptureError.nativeFailure
        }
    }

    public func stream(
        _ stream: SCStream,
        didOutputSampleBuffer sampleBuffer: CMSampleBuffer,
        of type: SCStreamOutputType
    ) {
        guard CMSampleBufferDataIsReady(sampleBuffer) else { return }
        switch type {
        case .screen: sink?.screenSample(sampleBuffer)
        case .audio: sink?.systemAudioSample(sampleBuffer)
        default: break
        }
    }

    public func stream(_ stream: SCStream, didStopWithError error: Error) {
        Task { @MainActor [weak self] in
            guard let self, self.stream === stream else { return }
            self.stream = nil
            self.microphone?.inputNode.removeTap(onBus: 0)
            self.microphone?.stop()
            self.microphone = nil
            self.sink?.mediaStopped(error: error)
        }
    }
}
#endif

#if os(iOS)
import ReplayKit

/// Receives borrowed ReplayKit samples. Consumers that retain a sample must
/// copy it, and they own encoding, persistence, transport, and session policy.
public protocol SystemDeviceCaptureSink: AnyObject {
    func receive(_ sample: CMSampleBuffer, kind: RPSampleBufferType)
    func mediaStopped(error: Error?)
}

/// Captures the owning application's screen and optional app/microphone audio.
/// ReplayKit owns the system consent UI; this library does not decide when a
/// caller may start capture or where captured samples go.
@MainActor public final class SystemDeviceCapture {
    private let recorder: RPScreenRecorder
    private weak var sink: SystemDeviceCaptureSink?
    private var active = false

    public init(
        sink: SystemDeviceCaptureSink,
        recorder: RPScreenRecorder = .shared()
    ) {
        self.sink = sink
        self.recorder = recorder
    }

    public func start(systemAudio: Bool = false, microphone: Bool = false) async throws {
        guard !active, recorder.isAvailable, !recorder.isRecording else {
            throw SystemMediaCaptureError.alreadyRunning
        }
        active = true
        recorder.isMicrophoneEnabled = microphone
        let sink = self.sink
        do {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                recorder.startCapture(handler: { sample, kind, error in
                    if let error {
                        sink?.mediaStopped(error: error)
                        return
                    }
                    let selected = kind == .video
                        || (kind == .audioApp && systemAudio)
                        || (kind == .audioMic && microphone)
                    if selected, CMSampleBufferDataIsReady(sample) {
                        sink?.receive(sample, kind: kind)
                    }
                }, completionHandler: { error in
                    if let error { continuation.resume(throwing: error) }
                    else { continuation.resume() }
                })
            }
            if !active {
                try await stopRecorder()
                throw CancellationError()
            }
        } catch {
            active = false
            throw error
        }
    }

    public func stop() async throws {
        guard active else { return }
        active = false
        do {
            try await stopRecorder()
            sink?.mediaStopped(error: nil)
        } catch {
            sink?.mediaStopped(error: error)
            throw SystemMediaCaptureError.nativeFailure
        }
    }

    private func stopRecorder() async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            recorder.stopCapture { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
    }
}

/// Base class for an app's ReplayKit Broadcast Upload Extension. Subclasses
/// supply a local sink; the library adds no authentication, network transport,
/// target selection, or authorization policy.
open class SystemBroadcastHandler: RPBroadcastSampleHandler {
    private var sink: SystemDeviceCaptureSink?
    private var paused = false

    open func makeSink(setupInfo: [String: NSObject]?) throws -> SystemDeviceCaptureSink {
        throw SystemMediaCaptureError.invalidConfiguration
    }

    open override func broadcastStarted(withSetupInfo setupInfo: [String: NSObject]?) {
        do { sink = try makeSink(setupInfo: setupInfo) }
        catch { finishBroadcastWithError(error) }
    }

    open override func processSampleBuffer(
        _ sampleBuffer: CMSampleBuffer,
        with sampleBufferType: RPSampleBufferType
    ) {
        guard !paused, CMSampleBufferDataIsReady(sampleBuffer) else { return }
        sink?.receive(sampleBuffer, kind: sampleBufferType)
    }

    open override func broadcastPaused() { paused = true }
    open override func broadcastResumed() { paused = false }
    open override func broadcastFinished() {
        sink?.mediaStopped(error: nil)
        sink = nil
    }
}
#endif
