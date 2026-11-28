package io.github.webong.ext.system.audio;

/**
 * The bounded, nonblocking write rule of a stereo 48 kHz 16-bit playback sink, apart from the device. A
 * closed sink is {@code canceled} before the packet is judged; then the packet is judged
 * ({@link PlaybackPacket#validate}); a full buffer that accepts nothing is {@code busy}, and a negative
 * native status is {@code io}. Partial acceptance is backpressure, not data loss: the caller keeps the
 * remainder and writes it later. Writes and close share a lock, so a write cannot race the close.
 */
public final class PlaybackSink implements AutoCloseable {
    private final PlaybackTrack track;
    private final int maxPacketBytes;
    private boolean closed;

    /** @throws IllegalArgumentException when the track is missing or the ceiling is not a whole frame or more */
    public PlaybackSink(PlaybackTrack track, int maxPacketBytes) {
        if (track == null) throw new IllegalArgumentException("playback track is required");
        if (maxPacketBytes < PlaybackPacket.BYTES_PER_FRAME) {
            throw new IllegalArgumentException("playback packet ceiling must hold a frame");
        }
        this.track = track;
        this.maxPacketBytes = maxPacketBytes;
    }

    /**
     * Writes {@code count} bytes from {@code offset} and returns how many the buffer accepted, at least one.
     *
     * @throws AudioException canceled, busy, io or unavailable
     * @throws PlaybackException invalid or too_large
     */
    public synchronized int write(byte[] pcm, int offset, int count) {
        if (closed) throw new AudioException("canceled", "audio playback is closed");
        PlaybackPacket.validate(pcm, offset, count, maxPacketBytes);
        int written = track.write(pcm, offset, count);
        if (written < 0) throw new AudioException("io", "native playback failed: " + written);
        if (written == 0) throw new AudioException("busy", "the playback buffer is full");
        return Math.min(written, count);
    }

    public int write(byte[] pcm) { return write(pcm, 0, pcm == null ? 0 : pcm.length); }

    public synchronized boolean isClosed() { return closed; }

    /** Releases the track once; closing again does nothing. */
    @Override public synchronized void close() {
        if (closed) return;
        closed = true;
        track.release();
    }
}
