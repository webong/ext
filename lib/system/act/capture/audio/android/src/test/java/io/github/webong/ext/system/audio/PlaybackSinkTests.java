package io.github.webong.ext.system.audio;

import io.github.webong.ext.system.fixtures.Fixtures;

import java.util.Set;

/**
 * The write codes of a playback sink with no device, as C# tests its sink: a closed sink is canceled
 * (before the packet is judged), a full buffer busy and a native failure io, all IllegalStateExceptions.
 */
public final class PlaybackSinkTests {
    private static void check(boolean value, String name) {
        if (!value) throw new AssertionError(name);
    }

    private static final class FakeTrack implements PlaybackTrack {
        int accept = Integer.MAX_VALUE;
        int releases;

        @Override public int write(byte[] pcm, int offset, int count) { return accept == Integer.MAX_VALUE ? count : accept; }
        @Override public void release() { releases++; }
    }

    private static String code(Runnable action) {
        try {
            action.run();
            return null;
        } catch (AudioException error) {
            check(error instanceof IllegalStateException, "an AudioException is an IllegalStateException");
            return error.code();
        } catch (PlaybackException error) {
            return error.code();
        }
    }

    public static void main(String[] arguments) throws Exception {
        Set<String> codes = Fixtures.codes();
        for (String code : new String[] {"busy", "canceled", "io", "unavailable", "invalid", "too_large"}) {
            check(codes.contains(code), code + " is in codes.json");
        }

        FakeTrack track = new FakeTrack();
        PlaybackSink sink = new PlaybackSink(track, PlaybackPacket.MAX_PACKET_BYTES);
        check(sink.write(new byte[8]) == 8, "a whole packet is accepted");
        track.accept = 4;
        check(sink.write(new byte[8]) == 4, "partial acceptance is backpressure");
        track.accept = 0;
        check("busy".equals(code(() -> sink.write(new byte[8]))), "a full buffer is busy");
        track.accept = -3;
        check("io".equals(code(() -> sink.write(new byte[8]))), "a native failure is io");
        track.accept = Integer.MAX_VALUE;
        check("invalid".equals(code(() -> sink.write(new byte[3]))), "a partial frame is invalid");
        check("too_large".equals(code(() -> sink.write(new byte[PlaybackPacket.MAX_PACKET_BYTES + 4]))), "an oversized packet is too_large");

        sink.close();
        sink.close();
        check(track.releases == 1, "close releases the track once");
        check(sink.isClosed(), "the sink is closed");
        check("canceled".equals(code(() -> sink.write(new byte[4]))), "a write after close is canceled");
        check("canceled".equals(code(() -> sink.write(new byte[3]))), "a closed sink is canceled before the packet is judged");

        try {
            PlaybackPacket.validate(new byte[3844], 0, 3844, 3840);
            check(false, "an oversized packet is refused");
        } catch (PlaybackException error) {
            check(error.getMessage().contains("3840"), "the too_large message names the ceiling: " + error.getMessage());
        }

        boolean refused = false;
        try { new PlaybackSink(track, 3); } catch (IllegalArgumentException expected) { refused = true; }
        check(refused, "a ceiling below one frame is refused");
        refused = false;
        try { new PlaybackSink(null, 4); } catch (IllegalArgumentException expected) { refused = true; }
        check(refused, "a sink needs a track");
        System.out.println("playback sink: ok");
    }

    private PlaybackSinkTests() {}
}
