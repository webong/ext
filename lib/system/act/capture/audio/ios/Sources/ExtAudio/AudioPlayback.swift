import AVFoundation
import Foundation

public enum SystemAudioPlaybackError: Error, Equatable {
    case invalidPacket
    case packetTooLarge
    case bufferFull
    case closed
    case nativeFailure

    /// The stable error code of `res/system/testdata/codes.json`.
    public var code: String {
        switch self {
        case .invalidPacket: return "invalid"
        case .packetTooLarge: return "too_large"
        case .bufferFull: return "busy"
        case .closed: return "canceled"
        case .nativeFailure: return "io"
        }
    }
}

/// Bounded PCM16 little-endian stereo 48 kHz playback. On iOS, the caller owns
/// AVAudioSession configuration. The library owns no stream, grant, or routing
/// policy.
public final class SystemAudioPlayback: @unchecked Sendable {
    public static let sampleRate: Double = 48_000
    public static let channels: AVAudioChannelCount = 2
    public static let bytesPerFrame = 4

    private let engine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private let lock = NSLock()
    private let maxPacketBytes: Int
    private let maxPendingPackets: Int
    private var pending = 0
    private var closed = false
    private let format = AVAudioFormat(
        standardFormatWithSampleRate: SystemAudioPlayback.sampleRate,
        channels: SystemAudioPlayback.channels
    )!

    /// Defaults allow a 20 ms packet and at most 240 ms of queued playback.
    public init(maxPacketBytes: Int = 3_840, maxPendingPackets: Int = 12) throws {
        guard maxPacketBytes > 0, maxPacketBytes <= 192_000,
              maxPacketBytes.isMultiple(of: Self.bytesPerFrame),
              (1...1_000).contains(maxPendingPackets) else {
            throw SystemAudioPlaybackError.invalidPacket
        }
        self.maxPacketBytes = maxPacketBytes
        self.maxPendingPackets = maxPendingPackets
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: format)
        do {
            try engine.start()
            player.play()
        } catch {
            throw SystemAudioPlaybackError.nativeFailure
        }
    }

    /// Queues one complete PCM packet or reports backpressure. Calls to write
    /// and close may come from different threads.
    public func write(_ pcm: Data) throws {
        try Self.validate(pcm, maxBytes: maxPacketBytes)
        lock.lock()
        guard !closed else {
            lock.unlock()
            throw SystemAudioPlaybackError.closed
        }
        guard pending < maxPendingPackets else {
            lock.unlock()
            throw SystemAudioPlaybackError.bufferFull
        }
        pending += 1
        lock.unlock()

        let frames = pcm.count / Self.bytesPerFrame
        guard let buffer = AVAudioPCMBuffer(
            pcmFormat: format,
            frameCapacity: AVAudioFrameCount(frames)
        ) else {
            decrementPending()
            throw SystemAudioPlaybackError.nativeFailure
        }
        buffer.frameLength = AVAudioFrameCount(frames)
        pcm.withUnsafeBytes { bytes in
            for frame in 0..<frames {
                for channel in 0..<Int(Self.channels) {
                    let offset = frame * Self.bytesPerFrame + channel * 2
                    let value = UInt16(bytes[offset]) | UInt16(bytes[offset + 1]) << 8
                    buffer.floatChannelData![channel][frame] = Float(Int16(bitPattern: value)) / 32_768
                }
            }
        }
        player.scheduleBuffer(buffer, completionCallbackType: .dataPlayedBack) { [weak self] _ in
            self?.decrementPending()
        }
    }

    public func close() {
        lock.lock()
        let wasClosed = closed
        closed = true
        lock.unlock()
        if !wasClosed {
            player.stop()
            engine.stop()
        }
    }

    deinit { close() }

    /// Checks one packet (`testdata/audio/v1`): it is not empty and is whole frames (`invalidPacket`),
    /// and is no larger than the ceiling (`packetTooLarge`); the frames are judged first.
    static func validate(_ pcm: Data, maxBytes: Int) throws {
        guard !pcm.isEmpty, pcm.count.isMultiple(of: bytesPerFrame) else { throw SystemAudioPlaybackError.invalidPacket }
        guard pcm.count <= maxBytes else { throw SystemAudioPlaybackError.packetTooLarge }
    }

    private func decrementPending() {
        lock.lock()
        pending = max(0, pending - 1)
        lock.unlock()
    }
}
