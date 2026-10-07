package io.github.webong.ext.plugin;

/** JNI entry points over the guest half of the shared C engine. */
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

    static native byte[] invoke(long handle, byte[] request, int timeoutMilliseconds);

    /** Strict JSON validation by the engine: depth, UTF-8, duplicate keys, trailing data. */
    static native int validateJson(byte[] data);

    static native void destroy(long handle);
}
