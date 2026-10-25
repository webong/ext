# Shipped inside the AAR as proguard.txt and applied to every app that depends on it.
# The JNI libraries find these members by name, which R8 cannot see, so shrinking or renaming
# them breaks the app at run time (NoSuchMethodError from NativeEngine.create, for example).
# Keep this list in step with the GetMethodID and FindClass calls in src/main/native/*.c.

# ext_jni.c: the guest engine calls the handler and throws its exception by name.
-keepclassmembers class io.github.webong.ext.plugin.Guest {
    byte[] dispatch(byte[], int, java.lang.Object);
}
-keep class io.github.webong.ext.plugin.GuestException {
    <init>(int);
}

# ext_wamr_jni.c: the WAMR host calls its policies and diagnostics, and throws by name. These
# rules cost nothing in an app that does not use the WAMR build.
-keepclassmembers class io.github.webong.ext.plugin.WamrPluginHost {
    boolean verify(byte[]);
    boolean authorize(byte[]);
    void diagnostic(byte[]);
}
-keep class io.github.webong.ext.plugin.WamrException {
    <init>(int, java.lang.String);
}

# Optional AndroidX document-start bridge uses these public APIs reflectively.
-keep class androidx.webkit.WebViewFeature { public static boolean isFeatureSupported(java.lang.String); }
-keep class androidx.webkit.WebViewCompat { public static *** addDocumentStartJavaScript(...); }
-keep interface androidx.webkit.ScriptHandler { public void remove(); }
