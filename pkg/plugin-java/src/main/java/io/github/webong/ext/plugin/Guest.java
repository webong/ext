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
    private static final int DEFAULT_MAX_CALL_MILLISECONDS = 30_000;

    private final Handler handler;
    private final Descriptor descriptor;
    private final ReadWriteLock lock = new ReentrantReadWriteLock();
    private long engine;

    public Guest(Descriptor descriptor, Handler handler) throws GuestException {
        this(descriptor, DEFAULT_MAX_CALL_MILLISECONDS, handler);
    }

    public Guest(Descriptor descriptor, int maxCallMilliseconds, Handler handler) throws GuestException {
        this.descriptor = descriptor;
        this.handler = handler;
        long created = NativeEngine.create(descriptor.toJson(), maxCallMilliseconds, this);
        if (created == 0) {
            throw new GuestException(-1);
        }
        this.engine = created;
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
        lock.readLock().lock();
        try {
            return NativeEngine.invoke(live(), request, timeoutMilliseconds);
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

    /** Called by the engine, on the calling thread: kind byte (0 payload, 1 error), then JSON. */
    byte[] dispatch(byte[] request, int remainingMilliseconds) throws Exception {
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
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        try {
            byte[] result = handler.handle(call);
            out.write(0);
            out.write(result == null || result.length == 0 ? "null".getBytes() : result);
        } catch (RemoteError error) {
            out.reset();
            out.write(1);
            out.write(error.toJson());
        }
        return out.toByteArray();
    }
}
