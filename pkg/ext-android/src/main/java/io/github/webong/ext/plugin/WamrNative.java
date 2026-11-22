package io.github.webong.ext.plugin;

/** JNI entry points over the host half of the shared C engine and its WAMR reactor backend. */
final class WamrNative {
    static {
        // An explicit path wins, for builds that keep the library outside java.library.path.
        String path = System.getenv("EXT_WAMR_JNI_LIBRARY");
        if (path != null && !path.isEmpty()) {
            System.load(path);
        } else {
            System.loadLibrary("extwamrjni");
        }
    }

    private WamrNative() {}

    static native long create(byte[] module, byte[] descriptor, int memoryPages, int stack,
            long instructionLimit, int maxDiagnostic, boolean wantDiagnostic, WamrPluginHost owner)
            throws WamrException;

    static native void start(long handle, int timeoutMilliseconds) throws WamrException;

    static native byte[] invoke(long handle, byte[] request, int timeoutMilliseconds) throws WamrException;

    static native byte[] inspect(byte[] module, int memoryPages, int stack, long instructionLimit,
            int timeoutMilliseconds) throws WamrException;

    /** Thread-safe and idempotent; interrupts a running call. */
    static native void close(long handle);

    /** After close, once no call is running. */
    static native void destroy(long handle);

    /** Reverse services (ext_host_set_services), before start. */
    static native void serve(long handle, int timeoutMilliseconds, int maxPayloadBytes, int maxInFlight)
            throws WamrException;

    /** A source of a registry (ext_registry_attach), before start. */
    static native void attach(long registry, long handle, int timeoutMilliseconds, int maxPayloadBytes,
            int maxInFlight) throws WamrException;

    /** ext_host_call: {contract, operation, payload?} in, a response envelope out. */
    static native byte[] call(long handle, byte[] input, int timeoutMilliseconds) throws WamrException;

    static native long registryCreate() throws WamrException;

    /** Once no host is attached to it or registered with it. */
    static native void registryDestroy(long registry);

    static native void registryRegister(long registry, String name, String version, long handle)
            throws WamrException;

    static native void registryUnregister(long registry, String name, String version) throws WamrException;

    /** from name, version, operation, then to name, version, operation. */
    static native void registryRoute(long registry, String[] names) throws WamrException;
}
