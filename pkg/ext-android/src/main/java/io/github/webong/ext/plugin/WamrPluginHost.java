package io.github.webong.ext.plugin;

import java.nio.charset.StandardCharsets;
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
        synchronized (this) {
            if (closed) {
                return;
            }
            closed = true;
            current = handle;
        }
        // Interrupt first, so a call that holds the read lock returns and the write lock is free.
        WamrNative.close(current);
        lock.writeLock().lock();
        try {
            handle = 0;
            WamrNative.destroy(current);
        } finally {
            lock.writeLock().unlock();
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
    private void diagnostic(byte[] text) {
        if (diagnostic != null) {
            diagnostic.accept(new String(text, StandardCharsets.UTF_8));
        }
    }
}
