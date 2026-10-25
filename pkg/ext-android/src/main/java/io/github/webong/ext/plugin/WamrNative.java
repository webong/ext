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
}
