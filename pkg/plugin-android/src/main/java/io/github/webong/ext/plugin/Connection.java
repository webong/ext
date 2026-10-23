package io.github.webong.ext.plugin;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * One JSON-line connection with its host, which a guest reads on a thread of its own so a
 * handler can wait for the host's answer to a reverse call while the connection is still
 * read (docs/plugin-guest-frames.md). The reader sends a request to the call loop
 * ({@link #next()}), an answer to the reverse call waiting for it, and anything else to a
 * violation that ends the session. Every frame the guest writes goes through one lock.
 */
final class Connection {
    private static final Pattern REQUEST_ID = Pattern.compile("g([0-9]+)");
    private static final long SESSION_REVERSE_MILLIS = 30_000;

    private record Reply(byte[] payload, RemoteError error) {}

    private final OutputStream out;
    private final Declarations declared;
    private final Object writeLock = new Object();
    private final Object lock = new Object();
    private final Map<String, CompletableFuture<Reply>> waiting = new HashMap<>();
    private final ArrayDeque<byte[]> requests = new ArrayDeque<>();
    private final ReverseCaller session = new Reverse(this, "", -1);
    private long issued;
    private int inFlight; // requests queued or being answered
    private boolean handshaking = true; // the hello is not answered yet
    private boolean ended;
    private boolean violated;
    private IOException failure;

    Connection(Declarations declared, OutputStream out) {
        this.declared = declared;
        this.out = out;
    }

    /** Reads frames until the connection ends, on a daemon thread, so a blocked read never holds the process. */
    void start(InputStream in) {
        Thread reader = new Thread(() -> read(in), "ext-plugin-reader");
        reader.setDaemon(true);
        reader.start();
    }

    private void read(InputStream in) {
        try {
            byte[] frame;
            while ((frame = JsonLine.readFrame(in)) != null) {
                route(frame);
            }
            end(null, false);
        } catch (IOException e) {
            end(e, false);
        } catch (Frames.Violation | RuntimeException e) {
            end(new IOException("plugin protocol violation"), true);
        }
    }

    private void route(byte[] frame) throws Frames.Violation {
        if (Json.member(frame, "frame") == null) {
            // A request, or something the engine will refuse. The host sends none during a call;
            // the one that may follow the hello right away is not a second call.
            synchronized (lock) {
                if (inFlight >= (handshaking ? 2 : 1)) {
                    throw new Frames.Violation();
                }
                inFlight++;
                requests.add(frame);
                lock.notifyAll();
            }
            return;
        }
        Object decoded = Frames.decode(frame);
        Map<String, Object> envelope = Json.object(decoded);
        if (envelope == null || !"reverse_response".equals(envelope.get("frame"))) {
            throw new Frames.Violation();
        }
        String id = envelope.get("id") instanceof String s ? s : "";
        CompletableFuture<Reply> waiter;
        long issuedNow;
        synchronized (lock) {
            waiter = waiting.get(id);
            issuedNow = issued;
        }
        if (waiter != null) {
            // Judged against the request it answers.
            Frames.Answer answer = Frames.reverseResponse(decoded, id);
            synchronized (lock) {
                waiting.remove(id);
            }
            waiter.complete(new Reply(answer.error() == null ? Json.member(frame, "payload") : null, answer.error()));
            return;
        }
        // An answer to a request that was abandoned is dropped; one to an id never issued is a violation.
        Matcher n = REQUEST_ID.matcher(id);
        if (!n.matches()) {
            throw new Frames.Violation();
        }
        try {
            long number = Long.parseLong(n.group(1));
            if (number < 1 || number > issuedNow) {
                throw new Frames.Violation();
            }
        } catch (NumberFormatException e) {
            throw new Frames.Violation();
        }
    }

    /** The next request, in the order the host sent them, or null once the connection has ended and none is left. */
    byte[] next() throws InterruptedException {
        synchronized (lock) {
            while (true) {
                if (violated) {
                    return null;
                }
                byte[] request = requests.poll();
                if (request != null) {
                    return request;
                }
                if (ended) {
                    return null;
                }
                lock.wait();
            }
        }
    }

    /** The request taken last is answered: the next one may come. */
    void idle() {
        synchronized (lock) {
            inFlight--;
            handshaking = false;
        }
    }

    /** Ends the connection, releasing every reverse call still waiting. The first reason stands. */
    void end(IOException reason, boolean violation) {
        List<CompletableFuture<Reply>> release;
        synchronized (lock) {
            if (ended) {
                return;
            }
            ended = true;
            violated = violation;
            failure = reason;
            release = new ArrayList<>(waiting.values());
            waiting.clear();
            lock.notifyAll();
        }
        for (CompletableFuture<Reply> waiter : release) {
            waiter.completeExceptionally(new FrameException(FrameException.Code.CLOSED));
        }
    }

    boolean ended() {
        synchronized (lock) {
            return ended;
        }
    }

    /** The host broke the framing rules. */
    boolean violated() {
        synchronized (lock) {
            return violated;
        }
    }

    /** Why the session ended badly (a violation, or a read that failed), or null. */
    IOException failure() {
        synchronized (lock) {
            return failure;
        }
    }

    /** Writes one frame; frames never interleave. */
    void write(byte[] frame) throws IOException {
        synchronized (writeLock) {
            out.write(frame);
            out.write('\n');
            out.flush();
        }
    }

    ReverseCaller session() {
        return session;
    }

    Declarations declared() {
        return declared;
    }

    /** A caller for the call whose request id is {@code parent}, bound by that call's deadline. */
    ReverseCaller forCall(String parent, long deadlineMillis, Call call) {
        return new Reverse(this, parent, deadlineMillis, call);
    }

    /**
     * Calls a host operation and waits for the answer. {@code parent} is the id of the call it
     * belongs to, empty for the session; {@code deadlineMillis} is when to give up.
     */
    byte[] reverse(String parent, ContractRef ref, String operation, byte[] payload, long deadlineMillis)
            throws RemoteError, FrameException {
        if (ref == null || operation == null || !declared.reverse(ref, operation)) {
            throw new RemoteError("reverse_undeclared", "reverse call refused");
        }
        CompletableFuture<Reply> answer = new CompletableFuture<>();
        String id;
        synchronized (lock) {
            id = "g" + (++issued);
        }
        byte[] frame = reverseRequest(id, parent, ref, operation, payload, deadlineMillis);
        synchronized (lock) {
            if (ended) {
                throw new FrameException(FrameException.Code.CLOSED);
            }
            waiting.put(id, answer);
        }
        try {
            write(frame);
        } catch (IOException e) {
            abandon(id);
            throw new FrameException(FrameException.Code.CLOSED);
        }
        Reply reply;
        try {
            reply = answer.get(Math.max(0, deadlineMillis - System.currentTimeMillis()), TimeUnit.MILLISECONDS);
        } catch (TimeoutException e) {
            abandon(id);
            throw new FrameException(FrameException.Code.DEADLINE);
        } catch (InterruptedException e) {
            abandon(id);
            Thread.currentThread().interrupt();
            throw new FrameException(FrameException.Code.CANCELED);
        } catch (ExecutionException e) {
            abandon(id);
            throw e.getCause() instanceof FrameException f ? f : new FrameException(FrameException.Code.CLOSED);
        }
        if (reply.error() != null) {
            throw reply.error();
        }
        return reply.payload();
    }

    private void abandon(String id) {
        synchronized (lock) {
            waiting.remove(id);
        }
    }

    private static byte[] reverseRequest(String id, String parent, ContractRef ref, String operation, byte[] payload,
            long deadlineMillis) throws FrameException {
        StringBuilder head = new StringBuilder("{\"frame\":\"reverse_request\",\"apiVersion\":\"ext.plugin/v1\",\"id\":");
        Json.quote(head, id);
        if (!parent.isEmpty()) {
            Json.quote(head.append(",\"parent\":"), parent);
        }
        Json.quote(head.append(",\"contract\":{\"name\":"), String.valueOf(ref.name()));
        Json.quote(head.append(",\"version\":"), String.valueOf(ref.version()));
        Json.quote(head.append("},\"operation\":"), operation);
        Json.quote(head.append(",\"deadline\":"), Instant.ofEpochMilli(deadlineMillis).toString());
        ByteArrayOutputStream frame = new ByteArrayOutputStream(head.length() + (payload == null ? 2 : payload.length + 12));
        frame.writeBytes(head.toString().getBytes(StandardCharsets.UTF_8));
        if (payload != null) {
            frame.writeBytes(",\"payload\":".getBytes(StandardCharsets.UTF_8));
            frame.writeBytes(payload);
        }
        frame.write('}');
        byte[] bytes = frame.toByteArray();
        try {
            if (bytes.length > JsonLine.MAX_FRAME) {
                throw new Frames.Violation();
            }
            Frames.reverseRequest(Frames.decode(bytes));
        } catch (Frames.Violation e) {
            throw new FrameException(FrameException.Code.INVALID);
        }
        return bytes;
    }

    /** A reverse caller: for one call (bounded by its deadline), or for the session (30 seconds a call). */
    static final class Reverse implements ReverseCaller {
        private final Connection connection;
        private final String parent;
        private final long deadlineMillis;
        private final Call call;

        Reverse(Connection connection, String parent, long deadlineMillis) {
            this(connection, parent, deadlineMillis, null);
        }

        Reverse(Connection connection, String parent, long deadlineMillis, Call call) {
            this.connection = connection;
            this.parent = parent;
            this.deadlineMillis = deadlineMillis;
            this.call = call;
        }

        @Override
        public byte[] call(ContractRef contract, String operation, byte[] payload) throws RemoteError, FrameException {
            return connection.reverse(parent, contract, operation, payload, bound(call == null ? SESSION_REVERSE_MILLIS : Long.MAX_VALUE));
        }

        @Override
        public byte[] call(ContractRef contract, String operation, byte[] payload, Duration timeout)
                throws RemoteError, FrameException {
            return connection.reverse(parent, contract, operation, payload, bound(Math.max(0, timeout.toMillis())));
        }

        // When to give up: after the wait asked for, and for a call never past its deadline.
        private long bound(long waitMillis) throws FrameException {
            if (call == null) {
                return System.currentTimeMillis() + waitMillis;
            }
            // A request names the call it belongs to, so it cannot outlive it.
            if (call.over()) {
                throw new FrameException(FrameException.Code.CLOSED);
            }
            long now = System.currentTimeMillis();
            return waitMillis >= deadlineMillis - now ? deadlineMillis : now + waitMillis;
        }
    }
}
