package io.github.webong.ext.plugin;

import java.util.ArrayList;
import java.util.List;

/**
 * One progress event of a call: a {@code progress} count of {@code completed} out of
 * {@code total} (zero when not known), or a {@code log} line, optionally on a stream
 * ({@code out} or {@code err}). Text is at most 2048 bytes of UTF-8; use {@link #split} to
 * cut a longer line.
 */
public record Progress(String kind, String stream, String text, long completed, long total) {
    /** The most text one event may carry, in bytes of UTF-8. */
    public static final int MAX_TEXT_BYTES = Frames.MAX_PROGRESS_TEXT;

    public static Progress log(String text) {
        return new Progress("log", "", text, 0, 0);
    }

    /** A log line of a program's standard output. */
    public static Progress out(String text) {
        return new Progress("log", "out", text, 0, 0);
    }

    /** A log line of a program's standard error. */
    public static Progress err(String text) {
        return new Progress("log", "err", text, 0, 0);
    }

    /** {@code completed} of {@code total}; zero when the total is not known. */
    public static Progress fraction(long completed, long total) {
        return new Progress("progress", "", "", completed, total);
    }

    /**
     * Cuts text into pieces that each fit one event: at most {@value #MAX_TEXT_BYTES} bytes of
     * UTF-8, never ending inside a character. A character that is not text (an unpaired
     * surrogate) becomes U+FFFD.
     */
    public static List<String> split(String text) {
        List<String> pieces = new ArrayList<>();
        StringBuilder piece = new StringBuilder();
        int bytes = 0;
        for (int i = 0; i < text.length(); ) {
            int cp = text.codePointAt(i);
            i += Character.charCount(cp);
            if (cp >= Character.MIN_SURROGATE && cp <= Character.MAX_SURROGATE) {
                cp = 0xFFFD;
            }
            int size = cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
            if (bytes + size > MAX_TEXT_BYTES) {
                pieces.add(piece.toString());
                piece.setLength(0);
                bytes = 0;
            }
            piece.appendCodePoint(cp);
            bytes += size;
        }
        if (piece.length() > 0) {
            pieces.add(piece.toString());
        }
        return pieces;
    }
}
