package io.github.webong.ext.system.audio;

/** The packet rule of a stereo 48 kHz 16-bit playback sink (testdata/audio/v1). */
public final class PlaybackPacket {
    /** One frame: two channels of 16-bit samples. */
    public static final int BYTES_PER_FRAME = 4;
    /** Twenty milliseconds. */
    public static final int MAX_PACKET_BYTES = 3_840;

    private PlaybackPacket() {}

    /**
     * Checks {@code count} bytes of {@code pcm} from {@code offset}: they must exist, be at least one frame
     * and whole frames (invalid), and be no more than {@code maxBytes} (too_large); the frames are judged
     * before the size.
     *
     * @throws PlaybackException when the packet is refused
     */
    public static void validate(byte[] pcm, int offset, int count, int maxBytes) {
        if (pcm == null || offset < 0 || count <= 0 || offset > pcm.length - count || count % BYTES_PER_FRAME != 0) {
            throw new PlaybackException("invalid", "invalid PCM packet");
        }
        if (count > maxBytes) {
            throw new PlaybackException("too_large", "PCM packet exceeds " + maxBytes + " bytes");
        }
    }
}
