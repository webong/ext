package io.github.webong.ext.system.audio;

/**
 * A playback sink that cannot take a packet, with the code C# {@code AudioException} and Swift
 * {@code SystemAudioPlaybackError} use (testdata/codes.json): {@code busy} (the playback buffer is
 * full), {@code canceled} (the sink is closed), {@code io} (the native write failed) or
 * {@code unavailable} (the native player cannot be opened or has gone). A refused packet is a
 * {@link PlaybackException} instead.
 */
public final class AudioException extends IllegalStateException {
    private static final long serialVersionUID = 1L;
    private final String code;

    public AudioException(String code, String message) {
        super(message);
        this.code = code;
    }

    public String code() { return code; }
}
