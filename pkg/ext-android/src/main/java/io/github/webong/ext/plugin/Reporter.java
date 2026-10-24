package io.github.webong.ext.plugin;

import java.io.IOException;
import java.nio.charset.StandardCharsets;

/** Sends the progress events of one call, within the rate limit the host set. */
final class Reporter {
    private final Connection connection;
    private final String parent;
    private final Frames.Limiter limiter;
    private boolean finished;

    Reporter(Connection connection, String parent, ProgressLimits limits) {
        this.connection = connection;
        this.parent = parent;
        this.limiter = new Frames.Limiter(limits.eventsPerSecond(), limits.bytesPerSecond(), System.nanoTime());
    }

    /** Events reach the host in the order they are reported, and none after {@link #finish()}. */
    synchronized ProgressOutcome report(Progress event) throws FrameException {
        Frames.Event e;
        try {
            if (event == null || event.kind() == null || event.stream() == null || event.text() == null
                    || event.completed() < 0 || event.total() < 0) {
                throw new Frames.Violation();
            }
            e = new Frames.Event(event.kind(), event.stream(), event.text(), event.completed(), event.total(), 0);
            Frames.validateEvent(e);
        } catch (Frames.Violation v) {
            throw new FrameException(FrameException.Code.INVALID);
        }
        long dropped = limiter.admit(Frames.utf8Length(e.text()), System.nanoTime());
        if (dropped < 0) {
            return ProgressOutcome.DROPPED;
        }
        if (finished) {
            throw new FrameException(FrameException.Code.CLOSED);
        }
        write(new Frames.Event(e.kind(), e.stream(), e.text(), e.completed(), e.total(), dropped));
        return ProgressOutcome.SENT;
    }

    /** Ends reporting after a marker for events never reported: nothing may follow the response. */
    synchronized void finish() {
        if (finished) {
            return;
        }
        finished = true;
        long pending = limiter.pending();
        if (pending > 0) {
            try {
                write(new Frames.Event("log", "", "", 0, 0, pending));
            } catch (FrameException ignored) {
                // The connection is gone; there is nobody to tell.
            }
        }
    }

    private void write(Frames.Event e) throws FrameException {
        StringBuilder frame = new StringBuilder("{\"frame\":\"progress\",\"apiVersion\":\"ext.plugin/v1\",\"parent\":");
        Json.quote(frame, parent).append(",\"kind\":");
        Json.quote(frame, e.kind());
        if (!e.stream().isEmpty()) {
            Json.quote(frame.append(",\"stream\":"), e.stream());
        }
        if (!e.text().isEmpty()) {
            Json.quote(frame.append(",\"text\":"), e.text());
        }
        if (e.completed() != 0) {
            frame.append(",\"completed\":").append(e.completed());
        }
        if (e.total() != 0) {
            frame.append(",\"total\":").append(e.total());
        }
        if (e.dropped() != 0) {
            frame.append(",\"dropped\":").append(Long.toUnsignedString(e.dropped()));
        }
        try {
            connection.write(frame.append('}').toString().getBytes(StandardCharsets.UTF_8));
        } catch (IOException io) {
            throw new FrameException(FrameException.Code.CLOSED);
        }
    }
}
