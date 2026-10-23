package io.github.webong.ext.plugin;

import java.time.DateTimeException;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * The rules for progress events and reverse calls (docs/plugin-guest-frames.md), which are
 * the Go contract's and are pinned by lib/plugin/testdata/v1/{frame-cases,
 * progress-limiter-cases}.json: what a frame may carry, the sender's rate limit and the
 * shape of the host's answer. The engine parses and validates the requests the host sends;
 * this judges the frames a guest writes and reads.
 */
final class Frames {
    private Frames() {}

    static final int MAX_PROGRESS_TEXT = 2048;
    /** A retry hint is whole milliseconds, at most one year. */
    static final long MAX_RETRY_AFTER_MS = 365L * 24 * 60 * 60 * 1000;
    static final long DEFAULT_PROGRESS_EVENTS = 50;
    static final long DEFAULT_PROGRESS_BYTES = 64 * 1024;

    private static final Pattern IDENTIFIER = Pattern.compile("[a-z0-9][a-z0-9._/-]{0,255}");
    private static final Pattern REVISION = Pattern.compile("[A-Za-z0-9][A-Za-z0-9._:+/-]{0,255}");
    private static final Pattern RFC3339 =
            Pattern.compile("(\\d{4})-(\\d\\d)-(\\d\\d)[Tt](\\d\\d):(\\d\\d):(\\d\\d)(\\.\\d+)?([Zz]|[+-]\\d\\d:\\d\\d)");
    private static final Pattern COUNT = Pattern.compile("0|[1-9][0-9]*");

    /** The host (or the guest's own frame) broke the framing rules. */
    static final class Violation extends Exception {
        private static final long serialVersionUID = 1L;

        Violation() {
            super("plugin protocol violation", null, false, false);
        }
    }

    /** A progress event as it travels in a frame: what a guest sends, with its drop count. */
    record Event(String kind, String stream, String text, long completed, long total, long dropped) {}

    /** A judged progress frame: the call it belongs to and the event. */
    record ProgressFrame(String parent, Event event) {}

    /** A judged reverse response: a payload (the caller reads its bytes) or an error. */
    record Answer(RemoteError error) {}

    // ---- reading JSON values ----

    static Map<String, Object> strictObject(Object value, Set<String> fields) throws Violation {
        Map<String, Object> map = Json.object(value);
        if (map == null || !fields.containsAll(map.keySet())) {
            throw new Violation();
        }
        return map;
    }

    /** A member Go decodes into a string: absent and null are empty. */
    static String text(Map<String, Object> map, String key) throws Violation {
        Object value = map.get(key);
        if (value == null || value == Json.NULL) {
            return "";
        }
        if (!(value instanceof String s)) {
            throw new Violation();
        }
        return s;
    }

    /** A member Go decodes into a uint64: absent and null are zero; anything but a whole number is refused. */
    static long count(Map<String, Object> map, String key) throws Violation {
        Object value = map.get(key);
        if (value == null || value == Json.NULL) {
            return 0;
        }
        if (!(value instanceof Json.Num n) || !COUNT.matcher(n.text()).matches()) {
            throw new Violation();
        }
        try {
            return Long.parseUnsignedLong(n.text());
        } catch (NumberFormatException e) {
            throw new Violation();
        }
    }

    static boolean identifier(String s) {
        return IDENTIFIER.matcher(s).matches();
    }

    static boolean revision(String s) {
        return REVISION.matcher(s).matches();
    }

    /** The length in bytes of text as UTF-8, or -1 when it is not text (an unpaired surrogate). */
    static int utf8Length(String text) {
        int bytes = 0;
        for (int i = 0; i < text.length(); ) {
            int cp = text.codePointAt(i);
            i += Character.charCount(cp);
            if (cp >= Character.MIN_SURROGATE && cp <= Character.MAX_SURROGATE) {
                return -1;
            }
            bytes += cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
        }
        return bytes;
    }

    /** An instant in RFC 3339 that is not Go's zero time. Returns milliseconds since the epoch. */
    static long instant(Object value) throws Violation {
        if (!(value instanceof String s)) {
            throw new Violation();
        }
        var m = RFC3339.matcher(s);
        if (!m.matches()) {
            throw new Violation();
        }
        try {
            int offset = 0;
            String zone = m.group(8);
            if (!zone.equalsIgnoreCase("z")) {
                int hours = Integer.parseInt(zone.substring(1, 3));
                int minutes = Integer.parseInt(zone.substring(4, 6));
                if (hours > 23 || minutes > 59) {
                    throw new Violation();
                }
                offset = (hours * 60 + minutes) * 60 * (zone.charAt(0) == '-' ? -1 : 1);
            }
            LocalDateTime t = LocalDateTime.of(Integer.parseInt(m.group(1)), Integer.parseInt(m.group(2)),
                    Integer.parseInt(m.group(3)), Integer.parseInt(m.group(4)), Integer.parseInt(m.group(5)),
                    Integer.parseInt(m.group(6)));
            long seconds = t.toEpochSecond(ZoneOffset.ofTotalSeconds(offset));
            long millis = 0;
            boolean fraction = false;
            if (m.group(7) != null) {
                String digits = m.group(7).substring(1);
                fraction = !digits.chars().allMatch(c -> c == '0');
                millis = Long.parseLong((digits + "000").substring(0, 3));
            }
            // Go's zero time is the instant 0001-01-01T00:00:00Z, in whatever zone it is written.
            if (seconds == LocalDateTime.of(1, 1, 1, 0, 0, 0).toEpochSecond(ZoneOffset.UTC) && !fraction) {
                throw new Violation();
            }
            return seconds * 1000 + millis;
        } catch (DateTimeException | NumberFormatException e) {
            throw new Violation();
        }
    }

    // ---- frames ----

    private static final Set<String> PROGRESS_FIELDS =
            Set.of("frame", "apiVersion", "parent", "kind", "stream", "text", "completed", "total", "dropped");

    /** Judges an event on its own. */
    static void validateEvent(Event e) throws Violation {
        int bytes = utf8Length(e.text());
        if (!(e.kind().equals("progress") || e.kind().equals("log"))
                || !(e.stream().isEmpty() || e.stream().equals("out") || e.stream().equals("err"))
                || (!e.stream().isEmpty() && !e.kind().equals("log"))
                || bytes < 0 || bytes > MAX_PROGRESS_TEXT
                || (e.total() != 0 && Long.compareUnsigned(e.completed(), e.total()) > 0)) {
            throw new Violation();
        }
    }

    /** Judges a progress frame: its parent (the call it belongs to) and the event. */
    static ProgressFrame progressFrame(Object value) throws Violation {
        Map<String, Object> m = strictObject(value, PROGRESS_FIELDS);
        String parent = text(m, "parent");
        Event event = new Event(text(m, "kind"), text(m, "stream"), text(m, "text"), count(m, "completed"),
                count(m, "total"), count(m, "dropped"));
        if (!text(m, "frame").equals("progress") || !text(m, "apiVersion").equals(Descriptor.API_VERSION)
                || !revision(parent)) {
            throw new Violation();
        }
        validateEvent(event);
        return new ProgressFrame(parent, event);
    }

    private static final Set<String> REVERSE_REQUEST_FIELDS =
            Set.of("frame", "apiVersion", "id", "parent", "contract", "operation", "deadline", "payload");

    /** Judges a reverse request frame, as the host reads it. */
    static void reverseRequest(Object value) throws Violation {
        Map<String, Object> m = strictObject(value, REVERSE_REQUEST_FIELDS);
        Map<String, Object> ref = strictObject(m.get("contract"), Set.of("name", "version"));
        String parent = text(m, "parent");
        if (!text(m, "frame").equals("reverse_request") || !text(m, "apiVersion").equals(Descriptor.API_VERSION)
                || !revision(text(m, "id")) || !(parent.isEmpty() || revision(parent))
                || !identifier(text(ref, "name")) || !revision(text(ref, "version"))
                || !identifier(text(m, "operation"))) {
            throw new Violation();
        }
        instant(m.get("deadline"));
    }

    private static final Set<String> REVERSE_RESPONSE_FIELDS = Set.of("frame", "apiVersion", "id", "payload", "error");

    /**
     * Judges a reverse response, which a guest accepts only for the request {@code id} it is
     * waiting on. The answer is the host's error, or null for a payload.
     */
    static Answer reverseResponse(Object value, String id) throws Violation {
        Map<String, Object> m = strictObject(value, REVERSE_RESPONSE_FIELDS);
        if (!text(m, "frame").equals("reverse_response") || !text(m, "apiVersion").equals(Descriptor.API_VERSION)
                || !text(m, "id").equals(id)) {
            throw new Violation();
        }
        boolean failed = m.get("error") != null && m.get("error") != Json.NULL;
        // A response carries a payload (null counts) or an error, never both or neither.
        if (failed == m.containsKey("payload")) {
            throw new Violation();
        }
        if (!failed) {
            return new Answer(null);
        }
        Map<String, Object> e = strictObject(m.get("error"), Set.of("code", "message", "retryAfterMilliseconds"));
        long retry = count(e, "retryAfterMilliseconds");
        String message = text(e, "message");
        int bytes = utf8Length(message);
        if (!identifier(text(e, "code")) || bytes < 0 || bytes > 4096 || retry < 0 || retry > MAX_RETRY_AFTER_MS) {
            throw new Violation();
        }
        return new Answer(new RemoteError(text(e, "code"), message, retry));
    }

    /** Parses a frame the way the engine reads one: strictly, then into values. */
    static Object decode(byte[] frame) throws Violation {
        if (NativeEngine.validateJson(frame) != 0) {
            throw new Violation();
        }
        try {
            return Json.parse(frame);
        } catch (RuntimeException e) {
            throw new Violation();
        }
    }

    // ---- the sender's rate limit ----

    /**
     * A token bucket counted in events and bytes of text, with a burst of twice the rate, and a
     * count of events dropped since the last one that passed. Time is a monotonic clock in
     * nanoseconds, given by the caller. Not thread safe.
     */
    static final class Limiter {
        private final double eventRate;
        private final double byteRate;
        private double events;
        private double bytes;
        private long last;
        private long dropped;

        Limiter(long eventsPerSecond, long bytesPerSecond, long nowNs) {
            eventRate = eventsPerSecond > 0 ? eventsPerSecond : DEFAULT_PROGRESS_EVENTS;
            byteRate = bytesPerSecond > 0 ? bytesPerSecond : DEFAULT_PROGRESS_BYTES;
            events = 2 * eventRate;
            bytes = 2 * byteRate;
            last = nowNs;
        }

        /**
         * The number of events dropped since the previous one that passed (which the caller
         * attaches to this event), or -1 when the rate limit drops it.
         */
        long admit(long textBytes, long nowNs) {
            long delta = nowNs - last;
            // Seconds as Go's Duration.Seconds computes them.
            double elapsed = (double) (delta / 1_000_000_000L) + (double) (delta % 1_000_000_000L) / 1e9;
            if (elapsed > 0) {
                events = Math.min(events + elapsed * eventRate, 2 * eventRate);
                bytes = Math.min(bytes + elapsed * byteRate, 2 * byteRate);
                last = nowNs;
            }
            if (events < 1 || bytes < (double) textBytes) {
                dropped++;
                return -1;
            }
            events--;
            bytes -= textBytes;
            long reported = dropped;
            dropped = 0;
            return reported;
        }

        /** The drops not yet reported, reset to zero. */
        long pending() {
            long reported = dropped;
            dropped = 0;
            return reported;
        }
    }

    /** The limits a request sets for a call: the rate with its defaults filled in. */
    static ProgressLimits resolve(Map<String, Object> rate) {
        long events = DEFAULT_PROGRESS_EVENTS;
        long bytes = DEFAULT_PROGRESS_BYTES;
        if (rate != null) {
            events = positive(rate.get("eventsPerSecond"), events);
            bytes = positive(rate.get("bytesPerSecond"), bytes);
        }
        return new ProgressLimits(events, bytes);
    }

    private static long positive(Object value, long fallback) {
        if (value instanceof Json.Num n) {
            try {
                long v = Long.parseLong(n.text());
                return v > 0 ? v : fallback;
            } catch (NumberFormatException e) {
                return fallback;
            }
        }
        return fallback;
    }
}
