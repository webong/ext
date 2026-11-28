package io.github.webong.ext.system.audio;

/** A refused playback packet; {@link #code()} is invalid or too_large (testdata/codes.json). */
public final class PlaybackException extends IllegalArgumentException {
    private static final long serialVersionUID = 1L;
    private final String code;

    public PlaybackException(String code, String message) {
        super(message);
        this.code = code;
    }

    public String code() { return code; }
}
