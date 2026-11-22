package io.github.webong.ext.plugin;

import java.util.HashMap;
import java.util.Map;

/**
 * A guest registry (ext_registry_* in ext_plugin_host.h) over WAMR hosts: a reverse call one guest makes
 * is served by a forward call to another guest, along an edge declared with {@link #route}. An
 * undeclared edge is refused with reverse_undeclared, an unregistered target with
 * reverse_unavailable, and a call that would re-enter a host already on its call chain with denied;
 * the target's answer passes through unchanged. A host is a source once {@link
 * WamrPluginHost#attach} names the registry, and a target once {@link #register} names a contract
 * it serves; it leaves the registry when it is closed. The native registry is freed once this
 * registry is closed and no host uses it any more.
 */
public final class WamrRegistry implements AutoCloseable {
    private final long handle;
    private final Map<String, WamrPluginHost> targets = new HashMap<>();
    private int users;
    private boolean closed;

    public WamrRegistry() throws WamrException {
        handle = WamrNative.registryCreate();
    }

    /** Names {@code host}, started and listing the contract, as the one serving {@code name}/{@code version}. */
    public void register(String name, String version, WamrPluginHost host) throws WamrException {
        synchronized (this) {
            if (closed) {
                throw new WamrException(WamrException.CLOSED, "closed");
            }
        }
        host.registerIn(this, name, version);
        synchronized (this) {
            targets.put(name + '\0' + version, host);
        }
    }

    /** Removes the contract and waits for the forward calls routed to its host. */
    public void unregister(String name, String version) throws WamrException {
        WamrNative.registryUnregister(handle, name, version);
        WamrPluginHost host;
        synchronized (this) {
            host = targets.remove(name + '\0' + version);
        }
        if (host != null) {
            host.forget(this, name, version);
        }
    }

    /** A reverse call of {@code fromOperation} in {@code from} is served by a call of {@code toOperation} in {@code to}. */
    public void route(String fromName, String fromVersion, String fromOperation, String toName, String toVersion,
            String toOperation) throws WamrException {
        WamrNative.registryRoute(handle, new String[] {fromName, fromVersion, fromOperation, toName, toVersion, toOperation});
    }

    /** Frees the registry once no host uses it. Idempotent. */
    @Override
    public void close() {
        synchronized (this) {
            closed = true;
        }
        freeIfUnused();
    }

    long handle() {
        return handle;
    }

    synchronized void retain() {
        users++;
    }

    void release() {
        synchronized (this) {
            users--;
        }
        freeIfUnused();
    }

    /** A target host is closing: it leaves the contract, which waits for the calls routed to it. */
    void leave(String name, String version) {
        try {
            WamrNative.registryUnregister(handle, name, version);
        } catch (WamrException e) {
            // Already gone: nothing to leave.
        }
        synchronized (this) {
            targets.remove(name + '\0' + version);
        }
    }

    private void freeIfUnused() {
        boolean free;
        synchronized (this) {
            free = closed && users == 0 && !freed;
            if (free) {
                freed = true;
            }
        }
        if (free) {
            WamrNative.registryDestroy(handle);
        }
    }

    private boolean freed;
}
