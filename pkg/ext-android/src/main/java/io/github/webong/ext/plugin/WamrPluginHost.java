package io.github.webong.ext.plugin;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.locks.ReentrantReadWriteLock;
import java.util.function.Consumer;
import java.util.function.Predicate;

/**
 * Hosts an ext.plugin/v1 WebAssembly reactor (docs/plugin-reactor-abi.md) in this process, on
 * the WAMR interpreter, through the shared engine's host. This is the fallback route of
 * docs/adr-mobile-wasm.md for apps that prefer not to use a WebView: it needs no UI, gives the
 * guest only the ABI's WASI imports, and enforces a deadline, a memory cap and an optional
 * instruction budget.
 *
 * <p>The host takes the descriptor it expects and refuses a module whose handshake differs.
 * Calls block the calling thread, so call from a background thread. A guest that traps,
 * overruns its deadline, runs out of memory or exits ends the whole instance. It needs the
 * {@code extwamrjni} library, built only when {@code EXT_WAMR_ROOT} names a WAMR checkout.
 */
public final class WamrPluginHost implements AutoCloseable {
    /** Limits for one instance. Zero means the engine's default. */
    public static final class Options {
        int memoryLimitPages;
        int stackSize;
        long instructionLimit;
        int maxDiagnosticBytes;

        /** Guest linear memory cap in 64 KiB pages; zero is 4096 (256 MiB). */
        public Options memoryLimitPages(int pages) {
            this.memoryLimitPages = pages;
            return this;
        }

        /** Bytes of interpreter stack; zero is 1 MiB. */
        public Options stackSize(int bytes) {
            this.stackSize = bytes;
            return this;
        }

        /** A budget of WebAssembly instructions per call, on top of the deadline; zero is none. */
        public Options instructionLimit(long instructions) {
            this.instructionLimit = instructions;
            return this;
        }

        /** Bytes of guest output passed to the diagnostic consumer over the instance's life. */
        public Options maxDiagnosticBytes(int bytes) {
            this.maxDiagnosticBytes = bytes;
            return this;
        }
    }

    /** What a reverse hook answers: a JSON payload (empty is null), or a public error. */
    public static final class ReverseReply {
        final boolean publicError;
        final byte[] json;

        private ReverseReply(boolean publicError, byte[] json) {
            this.publicError = publicError;
            this.json = json;
        }

        public static ReverseReply payload(byte[] json) {
            return new ReverseReply(false, json == null ? new byte[0] : json);
        }

        /** The JSON of a RemoteError, which the guest receives as it is if a peer would accept it. */
        public static ReverseReply publicError(byte[] json) {
            return new ReverseReply(true, json);
        }
    }

    /** Serves one reverse request: the Request JSON in, a reply out; throwing reaches the guest as operation_failed. */
    @FunctionalInterface
    public interface ReverseHandler {
        ReverseReply handle(byte[] request) throws Exception;
    }

    private volatile Predicate<byte[]> reverseAuthorize;
    private volatile ReverseHandler reverseHandle;
    private WamrRegistry sourceOf;
    private final List<String[]> registered = new ArrayList<>();
    private final List<WamrRegistry> registeredIn = new ArrayList<>();
    private final Predicate<byte[]> verify;
    private final Predicate<byte[]> authorize;
    private final Consumer<String> diagnostic;
    private final ReentrantReadWriteLock lock = new ReentrantReadWriteLock();
    private long handle;
    private boolean closed;

    /**
     * @param module the reactor module's bytes
     * @param descriptor the descriptor JSON the host expects; the handshake must match it exactly
     * @param verify runs before the module starts, with the descriptor; false refuses. Null allows.
     * @param authorize runs on every validated request; false denies it and leaves the host usable.
     *     Null allows.
     * @param diagnostic receives the guest's stdout and stderr, bounded; null discards them
     */
    public WamrPluginHost(byte[] module, byte[] descriptor, Options options, Predicate<byte[]> verify,
            Predicate<byte[]> authorize, Consumer<String> diagnostic) throws WamrException {
        Options o = options == null ? new Options() : options;
        this.verify = verify;
        this.authorize = authorize;
        this.diagnostic = diagnostic;
        this.handle = WamrNative.create(module, descriptor, o.memoryLimitPages, o.stackSize,
                o.instructionLimit, o.maxDiagnosticBytes, diagnostic != null, this);
    }

    public WamrPluginHost(byte[] module, byte[] descriptor) throws WamrException {
        this(module, descriptor, null, null, null, null);
    }

    /** Loads the module, instantiates it and checks its handshake against the expected descriptor. */
    public void start(long timeoutMilliseconds) throws WamrException {
        int timeout = milliseconds(timeoutMilliseconds);
        lock.readLock().lock();
        try {
            WamrNative.start(live(), timeout);
        } finally {
            lock.readLock().unlock();
        }
    }

    /**
     * Sends one complete ext.plugin/v1 request and returns the complete response, including
     * public errors. A call that fails after dispatch ends the session.
     */
    public byte[] invoke(byte[] request, long timeoutMilliseconds) throws WamrException {
        int timeout = milliseconds(timeoutMilliseconds);
        lock.readLock().lock();
        try {
            return WamrNative.invoke(live(), request, timeout);
        } finally {
            lock.readLock().unlock();
        }
    }

    /** Stops admission and interrupts a running call, then releases the host. Idempotent; any thread. */
    @Override
    public void close() {
        long current;
        List<String[]> leaving;
        List<WamrRegistry> leavingFrom;
        WamrRegistry source;
        synchronized (this) {
            if (closed) {
                return;
            }
            closed = true;
            current = handle;
            leaving = new ArrayList<>(registered);
            leavingFrom = new ArrayList<>(registeredIn);
            registered.clear();
            registeredIn.clear();
            source = sourceOf;
            sourceOf = null;
        }
        // Interrupt first, so a call that holds the read lock returns and the write lock is free.
        WamrNative.close(current);
        // Calls routed here have ended with the close; unregistering waits for them.
        for (int i = 0; i < leaving.size(); i++) {
            leavingFrom.get(i).leave(leaving.get(i)[0], leaving.get(i)[1]);
            leavingFrom.get(i).release();
        }
        lock.writeLock().lock();
        try {
            handle = 0;
            WamrNative.destroy(current);
        } finally {
            lock.writeLock().unlock();
        }
        if (source != null) {
            source.release();
        }
    }

    /**
     * Offers the guest the operations its descriptor lists under {@code reverse}, before {@link
     * #start}. The engine admits or refuses each request with a stable code; {@code authorize} and
     * {@code handle} get the Request JSON and are the host's policy. They run on the thread inside
     * {@link #invoke} or {@link #call}, must not use this host, and the call's deadline keeps running
     * while they do. Zero bounds use the defaults (30 s, 1 MiB, 4).
     */
    public void serve(Predicate<byte[]> authorize, ReverseHandler handle, long timeoutMilliseconds, int maxPayloadBytes,
            int maxInFlight) throws WamrException {
        reverseAuthorize = authorize;
        reverseHandle = handle;
        WamrNative.serve(live(), timeoutMilliseconds <= 0 ? 0 : milliseconds(timeoutMilliseconds), maxPayloadBytes, maxInFlight);
    }

    /**
     * Makes this host a source of {@code registry}, in place of {@link #serve} and before {@link
     * #start}: the guest's reverse calls are served by forward calls to the hosts registered there,
     * along the declared edges, after {@code authorize} allowed them.
     */
    public void attach(WamrRegistry registry, Predicate<byte[]> authorize, long timeoutMilliseconds, int maxPayloadBytes,
            int maxInFlight) throws WamrException {
        reverseAuthorize = authorize;
        WamrNative.attach(registry.handle(), live(), timeoutMilliseconds <= 0 ? 0 : milliseconds(timeoutMilliseconds),
                maxPayloadBytes, maxInFlight);
        registry.retain();
        WamrRegistry previous;
        synchronized (this) {
            previous = sourceOf;
            sourceOf = registry;
        }
        if (previous != null) {
            previous.release();
        }
    }

    /**
     * Calls an operation by contract and returns the complete response envelope; the engine supplies
     * the request's identity, surface, deadline and id. {@code payload} is JSON, or null for none.
     */
    public byte[] call(String contractName, String contractVersion, String operation, byte[] payload,
            long timeoutMilliseconds) throws WamrException {
        ByteArrayOutputStream input = new ByteArrayOutputStream();
        String head = "{\"contract\":{\"name\":\"" + contractName + "\",\"version\":\"" + contractVersion
                + "\"},\"operation\":\"" + operation + "\"";
        byte[] start = head.getBytes(StandardCharsets.UTF_8);
        input.write(start, 0, start.length);
        if (payload != null) {
            byte[] key = ",\"payload\":".getBytes(StandardCharsets.UTF_8);
            input.write(key, 0, key.length);
            input.write(payload, 0, payload.length);
        }
        input.write('}');
        int timeout = milliseconds(timeoutMilliseconds);
        lock.readLock().lock();
        try {
            return WamrNative.call(live(), input.toByteArray(), timeout);
        } finally {
            lock.readLock().unlock();
        }
    }

    void registerIn(WamrRegistry registry, String name, String version) throws WamrException {
        lock.readLock().lock();
        try {
            WamrNative.registryRegister(registry.handle(), name, version, live());
        } finally {
            lock.readLock().unlock();
        }
        registry.retain();
        synchronized (this) {
            registered.add(new String[] {name, version});
            registeredIn.add(registry);
        }
    }

    void forget(WamrRegistry registry, String name, String version) {
        boolean found = false;
        synchronized (this) {
            for (int i = 0; i < registered.size(); i++) {
                if (registeredIn.get(i) == registry && registered.get(i)[0].equals(name) && registered.get(i)[1].equals(version)) {
                    registered.remove(i);
                    registeredIn.remove(i);
                    found = true;
                    break;
                }
            }
        }
        if (found) {
            registry.release();
        }
    }

    /**
     * The module's own descriptor JSON, by loading and handshaking it once. This is inspection:
     * it asserts nothing about whether the module is trusted.
     */
    public static byte[] descriptorOf(byte[] module, long timeoutMilliseconds, Options options) throws WamrException {
        Options o = options == null ? new Options() : options;
        return WamrNative.inspect(module, o.memoryLimitPages, o.stackSize, o.instructionLimit,
                milliseconds(timeoutMilliseconds));
    }

    private synchronized long live() throws WamrException {
        if (closed) {
            throw new WamrException(WamrException.CLOSED, "closed");
        }
        return handle;
    }

    private static int milliseconds(long value) {
        return (int) Math.min(Math.max(value, 1), Integer.MAX_VALUE);
    }

    // Called from native code.
    @SuppressWarnings("unused")
    private boolean verify(byte[] descriptor) {
        return verify == null || verify.test(descriptor);
    }

    @SuppressWarnings("unused")
    private boolean authorize(byte[] request) {
        return authorize == null || authorize.test(request);
    }

    @SuppressWarnings("unused")
    private boolean reverseAuthorize(byte[] request) {
        Predicate<byte[]> check = reverseAuthorize;
        return check != null && check.test(request);
    }

    /** A kind byte (0 payload, 1 public error) and the reply's JSON; null or a throw is a failure. */
    @SuppressWarnings("unused")
    private byte[] reverseHandle(byte[] request) throws Exception {
        ReverseHandler handler = reverseHandle;
        if (handler == null) {
            return null;
        }
        ReverseReply reply = handler.handle(request);
        if (reply == null) {
            return null;
        }
        byte[] out = new byte[reply.json.length + 1];
        out[0] = (byte) (reply.publicError ? 1 : 0);
        System.arraycopy(reply.json, 0, out, 1, reply.json.length);
        return out;
    }

    @SuppressWarnings("unused")
    private void diagnostic(byte[] text) {
        if (diagnostic != null) {
            diagnostic.accept(new String(text, StandardCharsets.UTF_8));
        }
    }
}
