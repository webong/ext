package io.github.webong.ext.system.android;

import android.media.AudioAttributes;
import android.media.AudioFormat;
import android.media.AudioTrack;
import io.github.webong.ext.system.audio.AudioException;
import io.github.webong.ext.system.audio.PlaybackPacket;
import io.github.webong.ext.system.audio.PlaybackSink;
import io.github.webong.ext.system.audio.PlaybackTrack;

/**
 * Bounded PCM16 little-endian stereo 48 kHz playback. The caller owns audio
 * focus, routing policy, session authorization, and transport. Errors carry the
 * shared codes: {@link AudioException} unavailable when the player cannot be
 * opened, and the write codes of {@link PlaybackSink}.
 */
public final class SystemAudioPlayback implements AutoCloseable {
    public static final int SAMPLE_RATE = 48_000;
    public static final int BYTES_PER_FRAME = PlaybackPacket.BYTES_PER_FRAME;
    public static final int MAX_PACKET_BYTES = PlaybackPacket.MAX_PACKET_BYTES;

    private final PlaybackSink sink;

    /** @throws AudioException unavailable when the format, a bounded buffer or the native player is unavailable */
    public SystemAudioPlayback() {
        int minimum = AudioTrack.getMinBufferSize(
                SAMPLE_RATE, AudioFormat.CHANNEL_OUT_STEREO, AudioFormat.ENCODING_PCM_16BIT);
        if (minimum <= 0 || minimum > 192_000) {
            throw new AudioException("unavailable", "playback format or bounded buffer unavailable");
        }
        AudioTrack track;
        try {
            track = new AudioTrack.Builder()
                    .setAudioAttributes(new AudioAttributes.Builder()
                            .setUsage(AudioAttributes.USAGE_MEDIA)
                            .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                            .build())
                    .setAudioFormat(new AudioFormat.Builder()
                            .setSampleRate(SAMPLE_RATE)
                            .setChannelMask(AudioFormat.CHANNEL_OUT_STEREO)
                            .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                            .build())
                    .setBufferSizeInBytes(Math.max(minimum, 19_200))
                    .setTransferMode(AudioTrack.MODE_STREAM)
                    .build();
        } catch (UnsupportedOperationException exception) {
            throw new AudioException("unavailable", "native playback is unavailable");
        }
        try {
            if (track.getState() != AudioTrack.STATE_INITIALIZED) {
                throw new AudioException("unavailable", "native playback initialization failed");
            }
            track.play();
        } catch (IllegalStateException exception) {
            track.release();
            throw exception instanceof AudioException ? exception : new AudioException("unavailable", "native playback cannot start");
        } catch (RuntimeException exception) {
            track.release();
            throw exception;
        }
        sink = new PlaybackSink(new NativeTrack(track), MAX_PACKET_BYTES);
    }

    /**
     * Attempts a nonblocking write and returns the number of bytes accepted.
     * Partial acceptance is backpressure, not data loss; the caller retains the
     * remainder and retries it later.
     *
     * @throws AudioException canceled (closed), busy (the buffer is full), io or unavailable
     * @throws io.github.webong.ext.system.audio.PlaybackException invalid or too_large
     */
    public int write(byte[] pcm, int offset, int count) { return sink.write(pcm, offset, count); }

    public int write(byte[] pcm) { return sink.write(pcm); }

    @Override public void close() { sink.close(); }

    private static final class NativeTrack implements PlaybackTrack {
        private final AudioTrack track;

        NativeTrack(AudioTrack track) { this.track = track; }

        @Override public int write(byte[] pcm, int offset, int count) {
            int written = track.write(pcm, offset, count, AudioTrack.WRITE_NON_BLOCKING);
            if (written == AudioTrack.ERROR_DEAD_OBJECT) throw new AudioException("unavailable", "the native player has gone");
            return written;
        }

        @Override public void release() {
            try {
                track.pause();
                track.flush();
                track.stop();
            } catch (IllegalStateException ignored) {
                // A player that never started or has gone still has to be released.
            } finally {
                track.release();
            }
        }
    }
}
