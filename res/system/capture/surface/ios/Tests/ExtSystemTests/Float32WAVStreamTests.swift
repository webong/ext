import AVFoundation
import CoreMedia
import ExtSystem
import XCTest

final class Float32WAVStreamTests: XCTestCase {
    func testPlanarFloatWritesHeaderAndInterleavesChannels() throws {
        let input = try pcm(.pcmFormatFloat32, channels: 2, frames: 2)
        let channels = try XCTUnwrap(input.floatChannelData)
        channels[0][0] = 0.5
        channels[0][1] = -0.5
        channels[1][0] = 1
        channels[1][1] = -1

        var stream = try Float32WAVStream()
        let first = try stream.append(input)
        XCTAssertEqual(Array(first.prefix(44)), [
            0x52, 0x49, 0x46, 0x46, 0xff, 0xff, 0xff, 0xff,
            0x57, 0x41, 0x56, 0x45, 0x66, 0x6d, 0x74, 0x20,
            16, 0, 0, 0, 3, 0, 2, 0,
            0x80, 0xbb, 0, 0, 0, 0xdc, 5, 0,
            8, 0, 32, 0, 0x64, 0x61, 0x74, 0x61, 0xff, 0xff, 0xff, 0xff,
        ])
        XCTAssertEqual(samples(first.dropFirst(44)), [0.5, 1, -0.5, -1])
        XCTAssertEqual(samples(try stream.append(input)), [0.5, 1, -0.5, -1])
    }

    func testNativeSampleAndMicrophoneBufferShareTheSameStreamFormat() throws {
        let systemPCM = try pcm(.pcmFormatInt16, channels: 2, frames: 2, interleaved: true)
        let integers = try XCTUnwrap(systemPCM.int16ChannelData?.pointee)
        for (index, value) in [Int16.min, 16_384, 0, -8_192].enumerated() {
            integers[index] = value
        }
        let systemSample = try sampleBuffer(systemPCM)
        var stream = try Float32WAVStream()
        let first = try stream.append(systemSample)
        XCTAssertEqual(samples(first.dropFirst(44)), [-1, 0.5, 0, -0.25])

        let microphonePCM = try pcm(.pcmFormatFloat32, channels: 2, frames: 1, interleaved: true)
        let floats = try XCTUnwrap(microphonePCM.floatChannelData?.pointee)
        floats[0] = 0.25
        floats[1] = -0.25
        XCTAssertEqual(samples(try stream.append(microphonePCM)), [0.25, -0.25])
    }

    func testRemainingNativePCMFormatsAreConverted() throws {
        let intInput = try pcm(.pcmFormatInt32)
        try XCTUnwrap(intInput.int32ChannelData?.pointee)[0] = 1 << 29
        let doubleInput = try pcm(.pcmFormatFloat64)
        let doubles = try XCTUnwrap(
            doubleInput.mutableAudioBufferList.pointee.mBuffers.mData?.assumingMemoryBound(to: Double.self)
        )
        doubles[0] = -0.75
        var stream = try Float32WAVStream()
        XCTAssertEqual(samples(try stream.append(intInput).dropFirst(44)), [0.25])
        XCTAssertEqual(samples(try stream.append(doubleInput)), [-0.75])
    }

    func testExplicitChannelLayoutPreservesOrderAndCannotChangeMidstream() throws {
        let layout = try XCTUnwrap(AVAudioChannelLayout(layoutTag: kAudioChannelLayoutTag_MPEG_3_0_A))
        let format = AVAudioFormat(
            commonFormat: .pcmFormatFloat32, sampleRate: 48_000, interleaved: false, channelLayout: layout
        )
        let input = try XCTUnwrap(AVAudioPCMBuffer(pcmFormat: format, frameCapacity: 1))
        input.frameLength = 1
        let channels = try XCTUnwrap(input.floatChannelData)
        channels[0][0] = 0.25
        channels[1][0] = 0.5
        channels[2][0] = 0.75

        var stream = try Float32WAVStream()
        XCTAssertEqual(samples(try stream.append(input).dropFirst(44)), [0.25, 0.5, 0.75])

        let reordered = try XCTUnwrap(AVAudioChannelLayout(layoutTag: kAudioChannelLayoutTag_MPEG_3_0_B))
        let reorderedFormat = AVAudioFormat(
            commonFormat: .pcmFormatFloat32, sampleRate: 48_000, interleaved: false, channelLayout: reordered
        )
        let changed = try XCTUnwrap(AVAudioPCMBuffer(pcmFormat: reorderedFormat, frameCapacity: 1))
        changed.frameLength = 1
        XCTAssertThrowsError(try stream.append(changed)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .formatChanged)
        }
    }

    func testFrameAndByteBoundsIncludeTheFirstHeader() throws {
        for frames in [0, 192_001] {
            XCTAssertThrowsError(try Float32WAVStream(maxFrames: frames)) {
                XCTAssertEqual($0 as? Float32WAVStreamError, .invalidLimits)
            }
        }
        for bytes in [47, (8 << 20) + 1] {
            XCTAssertThrowsError(try Float32WAVStream(maxChunkBytes: bytes)) {
                XCTAssertEqual($0 as? Float32WAVStreamError, .invalidLimits)
            }
        }

        var stream = try Float32WAVStream(maxFrames: 2, maxChunkBytes: 48)
        let twoFrames = try pcm(.pcmFormatInt16, frames: 2)
        try XCTUnwrap(twoFrames.int16ChannelData?.pointee)[0] = 0
        try XCTUnwrap(twoFrames.int16ChannelData?.pointee)[1] = 0
        let oversizedSample = try sampleBuffer(twoFrames)
        XCTAssertThrowsError(try stream.append(oversizedSample)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .limitExceeded)
        }
        let oneFrame = try pcm(.pcmFormatFloat32)
        try XCTUnwrap(oneFrame.floatChannelData?.pointee)[0] = 0
        XCTAssertEqual(try stream.append(oneFrame).count, 48)
        XCTAssertEqual(try stream.append(twoFrames).count, 8)
        let threeFrames = try pcm(.pcmFormatFloat32, frames: 3)
        XCTAssertThrowsError(try stream.append(threeFrames)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .limitExceeded)
        }
    }

    func testFormatChangesAndInvalidSamplesLeaveStreamUsable() throws {
        var stream = try Float32WAVStream()
        let unsupported = try pcm(.pcmFormatFloat32, rate: 7_999)
        XCTAssertThrowsError(try stream.append(unsupported)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .unsupportedFormat)
        }
        let input = try pcm(.pcmFormatFloat32)
        try XCTUnwrap(input.floatChannelData?.pointee)[0] = 0.5
        let invalid = try sampleBuffer(input)
        XCTAssertEqual(CMSampleBufferInvalidate(invalid), noErr)
        XCTAssertThrowsError(try stream.append(invalid)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .invalidSample)
        }
        XCTAssertEqual(samples(try stream.append(input).dropFirst(44)), [0.5])

        let differentRate = try pcm(.pcmFormatFloat32, rate: 44_100)
        let differentChannels = try pcm(.pcmFormatFloat32, channels: 2)
        for changed in [differentRate, differentChannels] {
            XCTAssertThrowsError(try stream.append(changed)) {
                XCTAssertEqual($0 as? Float32WAVStreamError, .formatChanged)
            }
        }
        XCTAssertEqual(samples(try stream.append(input)), [0.5])
    }

    func testReadySampleWithMissingPCMDoesNotStartStream() throws {
        let input = try pcm(.pcmFormatFloat32)
        try XCTUnwrap(input.floatChannelData?.pointee)[0] = 0.5
        let missingData = try sampleBuffer(input, includeData: false)

        var stream = try Float32WAVStream()
        XCTAssertThrowsError(try stream.append(missingData)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .conversionFailed)
        }
        XCTAssertEqual(samples(try stream.append(input).dropFirst(44)), [0.5])
    }

    func testCloseIsIdempotentAndRejectsBothInputs() throws {
        let input = try pcm(.pcmFormatFloat32)
        try XCTUnwrap(input.floatChannelData?.pointee)[0] = 0
        let sample = try sampleBuffer(input)
        var stream = try Float32WAVStream()
        _ = try stream.append(input)
        stream.close()
        stream.close()
        XCTAssertThrowsError(try stream.append(input)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .closed)
        }
        XCTAssertThrowsError(try stream.append(sample)) {
            XCTAssertEqual($0 as? Float32WAVStreamError, .closed)
        }
    }

    private func pcm(
        _ encoding: AVAudioCommonFormat, rate: Double = 48_000, channels: AVAudioChannelCount = 1,
        frames: AVAudioFrameCount = 1, interleaved: Bool = false
    ) throws -> AVAudioPCMBuffer {
        let format = try XCTUnwrap(AVAudioFormat(
            commonFormat: encoding, sampleRate: rate, channels: channels, interleaved: interleaved
        ))
        let buffer = try XCTUnwrap(AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames))
        buffer.frameLength = frames
        return buffer
    }

    private func sampleBuffer(_ pcm: AVAudioPCMBuffer, includeData: Bool = true) throws -> CMSampleBuffer {
        var result: CMSampleBuffer?
        var timing = CMSampleTimingInfo(
            duration: CMTime(value: 1, timescale: Int32(pcm.format.sampleRate)),
            presentationTimeStamp: .zero, decodeTimeStamp: .invalid
        )
        XCTAssertEqual(CMSampleBufferCreate(
            allocator: kCFAllocatorDefault, dataBuffer: nil, dataReady: false,
            makeDataReadyCallback: nil, refcon: nil, formatDescription: pcm.format.formatDescription,
            sampleCount: Int(pcm.frameLength), sampleTimingEntryCount: 1, sampleTimingArray: &timing,
            sampleSizeEntryCount: 0, sampleSizeArray: nil, sampleBufferOut: &result
        ), noErr)
        let sample = try XCTUnwrap(result)
        if includeData {
            XCTAssertEqual(CMSampleBufferSetDataBufferFromAudioBufferList(
                sample, blockBufferAllocator: kCFAllocatorDefault, blockBufferMemoryAllocator: kCFAllocatorDefault,
                flags: 0, bufferList: pcm.audioBufferList
            ), noErr)
        }
        XCTAssertEqual(CMSampleBufferSetDataReady(sample), noErr)
        return sample
    }

    private func samples(_ data: Data) -> [Float] {
        data.withUnsafeBytes { bytes in
            stride(from: 0, to: bytes.count, by: 4).map { offset in
                let bits = bytes.loadUnaligned(fromByteOffset: offset, as: UInt32.self)
                return Float(bitPattern: UInt32(littleEndian: bits))
            }
        }
    }
}
