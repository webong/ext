package io.github.webong.ext.system.android;

import android.media.AudioAttributes;
import android.media.AudioFormat;
import android.media.AudioTrack;

/**
 * Bounded PCM16 little-endian stereo 48 kHz playback. The caller owns audio
 * focus, routing policy, session authorization, and transport.
 */
public final class SystemAudioPlayback implements AutoCloseable {
    public static final int SAMPLE_RATE = 48_000;
    public static final int BYTES_PER_FRAME = 4;
    public static final int MAX_PACKET_BYTES = 3_840;

    private final AudioTrack track;
    private boolean closed;

    public SystemAudioPlayback() {
        int minimum = AudioTrack.getMinBufferSize(
                SAMPLE_RATE, AudioFormat.CHANNEL_OUT_STEREO, AudioFormat.ENCODING_PCM_16BIT);
        if (minimum <= 0 || minimum > 192_000) {
            throw new IllegalStateException("playback format or bounded buffer unavailable");
        }
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
        try {
            if (track.getState() != AudioTrack.STATE_INITIALIZED) {
                throw new IllegalStateException("native playback initialization failed");
            }
            track.play();
        } catch (RuntimeException exception) {
            track.release();
            throw exception;
        }
    }

    /**
     * Attempts a nonblocking write and returns the number of bytes accepted.
     * Partial acceptance is backpressure, not data loss; the caller retains the
     * remainder and retries it later.
     */
    public synchronized int write(byte[] pcm, int offset, int count) {
        if (closed) throw new IllegalStateException("playback is closed");
        if (pcm == null || offset < 0 || count <= 0 || offset > pcm.length - count
                || count % BYTES_PER_FRAME != 0 || count > MAX_PACKET_BYTES) {
            throw new IllegalArgumentException("invalid PCM packet");
        }
        int written = track.write(pcm, offset, count, AudioTrack.WRITE_NON_BLOCKING);
        if (written < 0) throw new IllegalStateException("native playback failed: " + written);
        return written;
    }

    public int write(byte[] pcm) { return write(pcm, 0, pcm == null ? 0 : pcm.length); }

    @Override public synchronized void close() {
        if (closed) return;
        closed = true;
        try {
            track.pause();
            track.flush();
            track.stop();
        } finally {
            track.release();
        }
    }
}
