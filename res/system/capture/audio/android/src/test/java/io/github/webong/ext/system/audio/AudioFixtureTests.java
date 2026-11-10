package io.github.webong.ext.system.audio;

import io.github.webong.ext.system.fixtures.Fixtures;
import io.github.webong.ext.system.fixtures.Json;

import java.util.Map;
import java.util.Set;

/**
 * Runs the playback cases of testdata/audio/v1: the packet rule of a stereo 48 kHz 16-bit sink, which the
 * Go, Swift and C# bindings run as well. The other sections are Go's.
 */
public final class AudioFixtureTests {
    private static int checks;
    private static int failures;

    private static void check(boolean value, String name) {
        checks++;
        if (!value) {
            failures++;
            System.out.println("FAIL " + name);
        }
    }

    public static void main(String[] arguments) throws Exception {
        Map<String, Object> file = Fixtures.load("audio", 1);
        Set<String> codes = Fixtures.codes();
        Map<String, Object> defaults = Json.object(file.get("defaults"));
        check(PlaybackPacket.MAX_PACKET_BYTES == ((Long) defaults.get("playbackPacketBytes")).intValue(), "default packet ceiling");
        for (Object raw : Fixtures.cases(file, "playback")) {
            Map<String, Object> c = Json.object(raw);
            String name = "playback/" + c.get("name");
            String want = (String) c.get("code");
            String got = null;
            try {
                PlaybackPacket.validate(new byte[((Long) c.get("bytes")).intValue()], 0, ((Long) c.get("bytes")).intValue(), ((Long) c.get("maxBytes")).intValue());
            } catch (PlaybackException error) {
                got = error.code();
            }
            check(want == null || codes.contains(want), name + ": code " + want + " is not in codes.json");
            check(want == null ? got == null : want.equals(got), name + ": got " + got + ", want " + want);
        }
        // A packet read from the middle of a larger array, and a range that leaves it.
        PlaybackPacket.validate(new byte[16], 4, 8, 8);
        for (int[] range : new int[][] {{-1, 4}, {0, -4}, {13, 4}, {0, 20}}) {
            boolean refused = false;
            try { PlaybackPacket.validate(new byte[16], range[0], range[1], 3_840); } catch (PlaybackException error) { refused = "invalid".equals(error.code()); }
            check(refused, "range " + range[0] + "," + range[1]);
        }
        boolean refused = false;
        try { PlaybackPacket.validate(null, 0, 4, 3_840); } catch (PlaybackException error) { refused = "invalid".equals(error.code()); }
        check(refused, "no array");
        System.out.println(checks + " checks, " + failures + " failures");
        if (failures != 0) {
            System.exit(1);
        }
    }

    private AudioFixtureTests() {}
}
