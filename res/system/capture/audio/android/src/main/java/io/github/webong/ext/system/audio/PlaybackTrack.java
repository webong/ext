package io.github.webong.ext.system.audio;

/** The native side of a {@link PlaybackSink}: Android's AudioTrack, or a fake in a test. */
public interface PlaybackTrack {
    /**
     * Writes without blocking and returns the bytes accepted, or a negative native status. It may throw
     * an {@link AudioException} for a failure it can name, such as a player that has gone.
     */
    int write(byte[] pcm, int offset, int count);

    /** Stops playback and releases the native player. It is called once. */
    void release();
}
