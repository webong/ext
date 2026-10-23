package io.github.webong.ext.plugin;

import java.io.ByteArrayOutputStream;
import java.util.Map;
import java.util.concurrent.locks.ReadWriteLock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

/**
 * A plugin guest over the shared C engine. The engine does the strict JSON
 * parsing, request validation against the descriptor, deadlines and error
 * sanitizing; the application supplies a descriptor and a {@link Handler}.
 *
 * <p>A handler runs on the calling thread. It must not let an {@link Error}
 * escape expecting recovery: any throwable other than {@link RemoteError} is
 * reduced to a generic operation_failed response.
 */
public final class Guest implements AutoCloseable {
    static final int DEFAULT_MAX_CALL_MILLISECONDS = 30_000;

    private final Handler handler;
    private final Descriptor descriptor;
    private final Declarations declared;
    private final ReadWriteLock lock = new ReentrantReadWriteLock();
    private long engine;

    public Guest(Descriptor descriptor, Handler handler) throws GuestException {
        this(descriptor, DEFAULT_MAX_CALL_MILLISECONDS, handler);
    }

    public Guest(Descriptor descriptor, int maxCallMilliseconds, Handler handler) throws GuestException {
        this(descriptor, descriptor.toJson(), maxCallMilliseconds, handler);
    }

    /** A guest over a descriptor in JSON, which the engine validates; for tests of the wire model. */
    Guest(byte[] descriptorJson, Handler handler) throws GuestException {
        this(null, descriptorJson, DEFAULT_MAX_CALL_MILLISECONDS, handler);
    }

    private Guest(Descriptor descriptor, byte[] descriptorJson, int maxCallMilliseconds, Handler handler)
            throws GuestException {
        this.descriptor = descriptor;
        this.handler = handler;
        long created = NativeEngine.create(descriptorJson, maxCallMilliseconds, this);
        if (created == 0) {
            throw new GuestException(-1);
        }
        try {
            // What the engine accepted is what a call is judged by.
            this.declared = Declarations.of(NativeEngine.descriptor(created));
        } catch (Throwable t) {
            NativeEngine.destroy(created);
            throw t;
        }
        this.engine = created;
    }

    Declarations declared() {
        return declared;
    }

    public Descriptor descriptor() {
        return descriptor;
    }

    /** The descriptor exactly as the engine serves it to a handshake. */
    public byte[] descriptorJson() throws GuestException {
        lock.readLock().lock();
        try {
            return NativeEngine.descriptor(live());
        } finally {
            lock.readLock().unlock();
        }
    }

    /**
     * Validates a complete ext.plugin/v1 request and returns the complete response,
     * including public and sanitized private errors. Safe to call from many threads.
     */
    public byte[] invoke(byte[] request, int timeoutMilliseconds) throws GuestException {
        return invoke(request, timeoutMilliseconds, null);
    }

    /** As {@link #invoke(byte[], int)}, on a connection that carries frames, if there is one. */
    byte[] invoke(byte[] request, int timeoutMilliseconds, Connection connection) throws GuestException {
        lock.readLock().lock();
        try {
            return NativeEngine.invoke(live(), request, timeoutMilliseconds, connection);
        } finally {
            lock.readLock().unlock();
        }
    }

    public byte[] invoke(byte[] request) throws GuestException {
        return invoke(request, DEFAULT_MAX_CALL_MILLISECONDS);
    }

    /** Waits for calls in flight, then releases the engine. Idempotent. */
    @Override
    public void close() {
        lock.writeLock().lock();
        try {
            if (engine != 0) {
                NativeEngine.destroy(engine);
                engine = 0;
            }
        } finally {
            lock.writeLock().unlock();
        }
    }

    private long live() throws GuestException {
        if (engine == 0) {
            throw new GuestException(5); // EXT_CLOSED
        }
        return engine;
    }

    /**
     * Called by the engine, on the calling thread: kind byte (0 payload, 1 error), then JSON.
     * {@code state} is the connection the call came in on, or null.
     */
    byte[] dispatch(byte[] request, int remainingMilliseconds, Object state) throws Exception {
        Map<String, Object> envelope = Json.object(Json.parse(request));
        Map<String, Object> plugin = Json.object(envelope.get("plugin"));
        Map<String, Object> contract = Json.object(envelope.get("contract"));
        Call call = new Call(
                Json.string(envelope.get("id")),
                new Identity(Json.string(plugin.get("id")), Json.string(plugin.get("revision")), Json.string(plugin.get("version"))),
                new ContractRef(Json.string(contract.get("name")), Json.string(contract.get("version"))),
                Json.string(envelope.get("operation")),
                Json.string(envelope.get("surface")),
                Json.rawPayload(request),
                remainingMilliseconds);
        if (state instanceof Connection connection) {
            // A call gets a reporter when its operation declared progress and the host asked for events.
            Map<String, Object> rate = Json.object(envelope.get("progress"));
            ProgressLimits limits = rate != null && declared.progress(call.contract(), call.operation())
                    ? Frames.resolve(rate) : null;
            // A request the call makes of the host is bound by the call's own deadline, to the
            // millisecond below it.
            call.attach(connection, limits == null ? null : new Reporter(connection, call.id(), limits), limits,
                    Frames.instant(envelope.get("deadline")));
        }
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        try {
            byte[] result = handler.handle(call);
            out.write(0);
            out.write(result == null || result.length == 0 ? "null".getBytes() : result);
        } catch (RemoteError error) {
            out.reset();
            out.write(1);
            out.write(error.toJson());
        } finally {
            call.finish(); // before the response: nothing may follow it
        }
        return out.toByteArray();
    }
}
