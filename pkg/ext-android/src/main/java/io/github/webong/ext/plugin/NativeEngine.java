package io.github.webong.ext.plugin;

/** JNI entry points over the guest half of the shared C engine and over its package store. */
final class NativeEngine {
    static {
        // An explicit path wins, for builds that keep the library outside java.library.path.
        String path = System.getenv("EXT_JNI_LIBRARY");
        if (path != null && !path.isEmpty()) {
            System.load(path);
        } else {
            System.loadLibrary("extjni");
        }
    }

    private NativeEngine() {}

    static native long create(byte[] descriptor, int maxCallMilliseconds, Guest guest);

    static native byte[] descriptor(long handle);

    /** {@code state} reaches the handler with the call (the engine's call_user); it may be null. */
    static native byte[] invoke(long handle, byte[] request, int timeoutMilliseconds, Object state);

    /** Strict JSON validation by the engine: depth, UTF-8, duplicate keys, trailing data. */
    static native int validateJson(byte[] data);

    /**
     * Runs one of the engine's endpoint services ("endpoint.check" or
     * "endpoint.validate") and returns its ext_status; nothing of the input is
     * returned or thrown, because an endpoint is a credential.
     */
    static native int endpoint(String operation, byte[] input);

    static native void destroy(long handle);

    // The package store (ext_store_*, ext_layered_*). Paths and ids are UTF-8 bytes, because a
    // jstring would reach the engine as modified UTF-8. Every call reports a failure as a
    // GuestException carrying the ext_status. The handle must stay live for the whole call, and
    // is closed only after the calls in it have returned. The constants are those of the enums
    // in ext_store_jni.c.

    /** How {@link #storePut} puts a package in. */
    static final int PUT_INSTALL = 0;
    static final int PUT_UPGRADE = 1;
    static final int PUT_REPLACE = 2;
    /** The whole-root answers of {@link #storeReport} and {@link #layeredReport}. */
    static final int REPORT_IDS = 0;
    static final int REPORT_LIST = 1;
    static final int REPORT_VERIFY = 2;
    static final int REPORT_UNVERIFIED = 3;

    static native long storeOpen(byte[] root, long maxArtifactBytes, int maxTreeFiles, long maxTreeBytes)
            throws GuestException;

    static native void storeClose(long handle);

    static native byte[] storePut(long handle, int how, byte[] source, int flags) throws GuestException;

    static native void storeRemove(long handle, byte[] id) throws GuestException;

    static native byte[] storeLoad(long handle, byte[] id) throws GuestException;

    static native byte[] storeReport(long handle, int kind) throws GuestException;

    static native long layeredOpen(byte[][] roots, long maxArtifactBytes, int maxTreeFiles, long maxTreeBytes)
            throws GuestException;

    static native void layeredClose(long handle);

    /** {@code layer[0]} receives the position of the root that served the package. */
    static native byte[] layeredLoad(long handle, byte[] id, int[] layer) throws GuestException;

    static native byte[] layeredReport(long handle, int kind) throws GuestException;
}
