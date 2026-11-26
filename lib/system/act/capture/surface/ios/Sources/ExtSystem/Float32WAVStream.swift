import AVFoundation
import CoreMedia
import Foundation

public enum Float32WAVStreamError: Error, Equatable {
    case invalidLimits
    case invalidSample
    case unsupportedFormat
    case formatChanged
    case limitExceeded
    case conversionFailed
    case closed
}

/// Converts borrowed Apple capture buffers into chunks of streaming RIFF/WAVE.
/// The first successful append emits a 44-byte IEEE float header with unknown
/// RIFF and data lengths (UInt32.max). Every chunk contains little-endian,
/// interleaved Float32 samples in source channel order. The stream preserves
/// the sample rate and channel count and requires a consistent channel layout.
/// The WAV header carries the channel count without a speaker mask.
///
/// This framing is for readers that accept unknown WAV lengths. Write chunks
/// in append order and signal end of stream to the reader yourself. Serialize
/// access when using callbacks from multiple queues.
public struct Float32WAVStream {
    private static let headerBytes = 44
    private static let bytesPerSample = 4

    private let maxFrames: Int
    private let maxChunkBytes: Int
    private var outputFormat: AVAudioFormat?
    private var sourceLayout: AVAudioChannelLayout?
    private var closed = false

    /// Bounds each append to at most 192,000 frames and 8 MiB, including any
    /// header. Supported input is native linear PCM at an integral sample
    /// rate of 8–192 kHz with 1–8 channels.
    public init(maxFrames: Int = 192_000, maxChunkBytes: Int = 8 << 20) throws {
        let minimumChunkBytes = Self.headerBytes + Self.bytesPerSample
        guard (1...192_000).contains(maxFrames), (minimumChunkBytes...(8 << 20)).contains(maxChunkBytes) else {
            throw Float32WAVStreamError.invalidLimits
        }
        self.maxFrames = maxFrames
        self.maxChunkBytes = maxChunkBytes
    }

    /// Encodes a microphone PCM buffer. An error leaves the stream unchanged.
    public mutating func append(_ buffer: AVAudioPCMBuffer) throws -> Data {
        let byteCount = try payloadSize(format: buffer.format, frames: Int(buffer.frameLength))
        return try encode(buffer, byteCount: byteCount)
    }

    /// Encodes a ready linear PCM sample from ScreenCaptureKit or ReplayKit.
    /// Frame and output byte bounds are checked before copying the sample.
    public mutating func append(_ sample: CMSampleBuffer) throws -> Data {
        guard !closed else { throw Float32WAVStreamError.closed }
        guard CMSampleBufferIsValid(sample), CMSampleBufferDataIsReady(sample),
              let description = CMSampleBufferGetFormatDescription(sample) else {
            throw Float32WAVStreamError.invalidSample
        }
        guard CMFormatDescriptionGetMediaType(description) == kCMMediaType_Audio,
              let stream = CMAudioFormatDescriptionGetStreamBasicDescription(description),
              stream.pointee.mFormatID == kAudioFormatLinearPCM else {
            throw Float32WAVStreamError.unsupportedFormat
        }
        let format = AVAudioFormat(cmAudioFormatDescription: description)
        let frames = CMSampleBufferGetNumSamples(sample)
        let byteCount = try payloadSize(format: format, frames: frames)
        guard let input = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(frames)) else {
            throw Float32WAVStreamError.conversionFailed
        }
        input.frameLength = AVAudioFrameCount(frames)
        guard CMSampleBufferCopyPCMDataIntoAudioBufferList(
            sample, at: 0, frameCount: Int32(frames), into: input.mutableAudioBufferList
        ) == noErr else {
            throw Float32WAVStreamError.conversionFailed
        }
        return try encode(input, byteCount: byteCount)
    }

    /// Ends the stream, including when the caller cancels capture. Closing is
    /// idempotent and emits no trailer. Later appends fail with `.closed`.
    public mutating func close() {
        closed = true
    }

    private func payloadSize(format: AVAudioFormat, frames: Int) throws -> Int {
        guard !closed else { throw Float32WAVStreamError.closed }
        guard frames > 0 else { throw Float32WAVStreamError.invalidSample }
        let rate = format.sampleRate
        guard (8_000...192_000).contains(rate), rate.rounded() == rate,
              (1...8).contains(format.channelCount) else {
            throw Float32WAVStreamError.unsupportedFormat
        }
        switch format.commonFormat {
        case .pcmFormatFloat32, .pcmFormatFloat64, .pcmFormatInt16, .pcmFormatInt32:
            break
        default:
            throw Float32WAVStreamError.unsupportedFormat
        }
        if let outputFormat, rate != outputFormat.sampleRate
            || format.channelCount != outputFormat.channelCount || format.channelLayout != sourceLayout {
            throw Float32WAVStreamError.formatChanged
        }
        let headerBytes = outputFormat == nil ? Self.headerBytes : 0
        let bytesPerFrame = Int(format.channelCount) * Self.bytesPerSample
        guard frames <= maxFrames, frames <= (maxChunkBytes - headerBytes) / bytesPerFrame else {
            throw Float32WAVStreamError.limitExceeded
        }
        return frames * bytesPerFrame
    }

    private mutating func encode(_ input: AVAudioPCMBuffer, byteCount: Int) throws -> Data {
        let format = try outputFormat ?? Self.interleavedFormat(for: input.format)
        let output: AVAudioPCMBuffer
        if input.format == format {
            output = input
        } else {
            guard let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: input.frameLength),
                  let converter = AVAudioConverter(from: input.format, to: format) else {
                throw Float32WAVStreamError.conversionFailed
            }
            do {
                try converter.convert(to: buffer, from: input)
            } catch {
                throw Float32WAVStreamError.conversionFailed
            }
            output = buffer
        }
        guard output.frameLength == input.frameLength,
              let samples = output.floatChannelData?.pointee else {
            throw Float32WAVStreamError.conversionFailed
        }

        let firstChunk = outputFormat == nil
        var chunk = Data(capacity: byteCount + (firstChunk ? Self.headerBytes : 0))
        if firstChunk {
            Self.appendHeader(to: &chunk, rate: UInt32(format.sampleRate), channels: UInt16(format.channelCount))
        }
        // Apple targets are little-endian, as required by RIFF.
        let bytes = UnsafeRawBufferPointer(start: samples, count: byteCount)
        chunk.append(contentsOf: bytes)
        if firstChunk {
            outputFormat = format
            sourceLayout = input.format.channelLayout
        }
        return chunk
    }

    private static func interleavedFormat(for source: AVAudioFormat) throws -> AVAudioFormat {
        if let layout = source.channelLayout {
            return AVAudioFormat(
                commonFormat: .pcmFormatFloat32, sampleRate: source.sampleRate,
                interleaved: true, channelLayout: layout
            )
        }
        guard let format = AVAudioFormat(
            commonFormat: .pcmFormatFloat32, sampleRate: source.sampleRate,
            channels: source.channelCount, interleaved: true
        ) else {
            throw Float32WAVStreamError.unsupportedFormat
        }
        return format
    }

    private static func appendHeader(to data: inout Data, rate: UInt32, channels: UInt16) {
        func append<T: FixedWidthInteger>(_ value: T) {
            withUnsafeBytes(of: value.littleEndian) { data.append(contentsOf: $0) }
        }
        let bytesPerFrame = channels * UInt16(bytesPerSample)
        data.append(contentsOf: "RIFF".utf8)
        append(UInt32.max)
        data.append(contentsOf: "WAVEfmt ".utf8)
        append(UInt32(16))
        append(UInt16(3)) // WAVE_FORMAT_IEEE_FLOAT
        append(channels)
        append(rate)
        append(rate * UInt32(bytesPerFrame))
        append(bytesPerFrame)
        append(UInt16(32))
        data.append(contentsOf: "data".utf8)
        append(UInt32.max)
    }
}
