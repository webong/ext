package io.github.webong.ext.web.android;

import android.webkit.WebView;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.util.Collections;
import java.util.Set;

/** Optional AndroidX WebKit linkage, isolated in the native source set. No late-injection fallback. */
final class DocumentStart {
    private final Method supported;
    private final Method add;
    private final Method remove;

    DocumentStart() {
        Method s = null, a = null, r = null;
        try {
            s = Class.forName("androidx.webkit.WebViewFeature").getMethod("isFeatureSupported", String.class);
            a = Class.forName("androidx.webkit.WebViewCompat").getMethod("addDocumentStartJavaScript",
                    WebView.class, String.class, Set.class);
            r = Class.forName("androidx.webkit.ScriptHandler").getMethod("remove");
        } catch (ReflectiveOperationException | LinkageError absent) {
            s = null; a = null; r = null;
        }
        supported = s; add = a; remove = r;
    }

    boolean available() {
        return supported != null && Boolean.TRUE.equals(invoke(supported, null, "DOCUMENT_START_SCRIPT"));
    }

    Object add(WebView view, String source) {
        // ReplaySource enforces full URL matches/exclusions in every frame, including path/query.
        return invoke(add, null, view, source, Collections.singleton("*"));
    }

    void remove(Object handler) { invoke(remove, handler); }

    private static Object invoke(Method method, Object receiver, Object... args) {
        try {
            return method.invoke(receiver, args);
        } catch (InvocationTargetException failure) {
            throw new IllegalStateException("AndroidX document-start operation failed", failure.getCause());
        } catch (ReflectiveOperationException failure) {
            throw new IllegalStateException("AndroidX document-start API unavailable", failure);
        }
    }
}
