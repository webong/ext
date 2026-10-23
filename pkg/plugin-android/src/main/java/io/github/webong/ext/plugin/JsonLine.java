package io.github.webong.ext.plugin;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.Set;

/**
 * The ext.plugin/v1 JSON-line transport for a guest process: one JSON frame per
 * line, a plugin.hello frame first, then one response per request. Stdout belongs
 * to the protocol; log to stderr.
 *
 * <p>The input is read on a thread of its own, so a handler can report progress
 * ({@link Call#progress}) and call its host ({@link Call#reverse}) and still have the host's
 * answers read. A frame the host may not send, such as a second request while a call is in
 * flight, ends the session with an {@link IOException} and the call in flight is not answered.
 * When the input ends, the call in flight is answered and the session ends.
 */
public final class JsonLine {
    static final int MAX_FRAME = 24 * 1024 * 1024;
    private static final Set<String> HELLO_FIELDS =
            Set.of("apiVersion", "id", "operation", "deadline", "plugin", "contract", "surface");

    private JsonLine() {}

    /** Serves the guest on standard input and output until the host closes the pipe. */
    public static void serveStdio(Guest guest) throws IOException, GuestException {
        serve(guest, System.in, new java.io.FileOutputStream(java.io.FileDescriptor.out));
    }

    /**
     * Serves until end of input. A malformed frame ends the session with an
     * exception and writes nothing, so the host sees a failed process.
     */
    public static void serve(Guest guest, InputStream in, OutputStream out) throws IOException, GuestException {
        Connection connection = new Connection(guest.declared(), out);
        connection.start(in);
        try {
            boolean hello = false;
            byte[] frame;
            while ((frame = nextRequest(connection)) != null) {
                byte[] response;
                if (!hello) {
                    checkHello(frame);
                    hello = true;
                    ByteArrayOutputStream body = new ByteArrayOutputStream();
                    body.write("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"hello\",\"payload\":".getBytes(StandardCharsets.UTF_8));
                    body.write(guest.descriptorJson());
                    body.write('}');
                    response = body.toByteArray();
                } else {
                    response = guest.invoke(frame, Guest.DEFAULT_MAX_CALL_MILLISECONDS, connection);
                }
                // A violation found while the handler ran ends the session, and the call goes unanswered.
                if (connection.violated()) {
                    break;
                }
                connection.idle(); // before the response, so the next request is never early
                connection.write(response);
            }
            IOException failure = connection.failure();
            if (failure != null) {
                throw failure;
            }
        } finally {
            connection.end(null, false);
        }
    }

    private static byte[] nextRequest(Connection connection) throws IOException {
        try {
            return connection.next();
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new java.io.InterruptedIOException("interrupted while waiting for a request");
        }
    }

    static byte[] readFrame(InputStream in) throws IOException {
        ByteArrayOutputStream line = new ByteArrayOutputStream();
        int b;
        while ((b = in.read()) >= 0) {
            if (b == '\n') {
                return line.toByteArray();
            }
            if (line.size() > MAX_FRAME) {
                throw new IOException("frame too large");
            }
            line.write(b);
        }
        if (line.size() == 0) {
            return null;
        }
        throw new IOException("truncated frame");
    }

    private static void checkHello(byte[] frame) throws IOException {
        if (NativeEngine.validateJson(frame) != 0) {
            throw new IOException("invalid hello");
        }
        Map<String, Object> hello = Json.object(Json.parse(frame));
        if (hello == null || !HELLO_FIELDS.containsAll(hello.keySet())
                || !"ext.plugin/v1".equals(hello.get("apiVersion"))
                || !"hello".equals(hello.get("id"))
                || !"plugin.hello".equals(hello.get("operation"))
                || !(hello.get("deadline") instanceof String deadline)
                || !emptyObject(hello.get("plugin")) || !emptyObject(hello.get("contract"))
                || !(hello.get("surface") == null || "".equals(hello.get("surface")))) {
            throw new IOException("invalid hello");
        }
        try {
            if (!OffsetDateTime.parse(deadline).toInstant().isAfter(java.time.Instant.now())) {
                throw new IOException("hello deadline passed");
            }
        } catch (java.time.format.DateTimeParseException e) {
            throw new IOException("invalid hello deadline");
        }
    }

    private static boolean emptyObject(Object value) {
        if (value == null) {
            return true;
        }
        Map<String, Object> map = Json.object(value);
        if (map == null) {
            return false;
        }
        for (Object field : map.values()) {
            if (!"".equals(field)) {
                return false;
            }
        }
        return true;
    }
}
