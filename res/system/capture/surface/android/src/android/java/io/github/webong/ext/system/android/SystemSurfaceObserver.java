package io.github.webong.ext.system.android;

import android.app.Activity;
import android.content.Context;
import android.hardware.display.DisplayManager;
import android.os.Looper;
import android.os.Process;
import android.util.DisplayMetrics;
import android.view.Display;
import android.view.View;
import android.view.Window;
import io.github.webong.ext.system.DisplayInfo;
import io.github.webong.ext.system.SurfaceBounds;
import io.github.webong.ext.system.SurfaceCollectionStatus;
import io.github.webong.ext.system.SurfaceOptions;
import io.github.webong.ext.system.SurfaceSnapshot;
import io.github.webong.ext.system.WindowInfo;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.TimeZone;

/** Reads app-visible Android display and window geometry on the main thread. */
public final class SystemSurfaceObserver {
    private final Context context;
    private final SurfaceOptions options;
    private final String processStartId;

    public SystemSurfaceObserver(Context context) { this(context, new SurfaceOptions()); }

    public SystemSurfaceObserver(Context context, SurfaceOptions options) {
        if (context == null || options == null) throw new IllegalArgumentException("context and options are required");
        this.context = context.getApplicationContext();
        this.options = options;
        this.processStartId = Process.myPid() + ":" + Process.getStartElapsedRealtime();
    }

    /**
     * Observes displays and the supplied Activity's app-owned window. Pass null
     * when only display metadata is needed.
     */
    @SuppressWarnings("deprecation") // Display#getRealMetrics keeps the binding usable from API 24.
    public SurfaceSnapshot observe(Activity activity) {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            throw new IllegalStateException("surface observation must run on the Android main thread");
        }
        String now = timestamp();
        DisplayManager manager = (DisplayManager) context.getSystemService(Context.DISPLAY_SERVICE);
        Display[] nativeDisplays = manager == null ? new Display[0] : manager.getDisplays();
        boolean displaysLimited = nativeDisplays.length > options.maxDisplays();
        List<DisplayInfo> displays = new ArrayList<>();
        for (int index = 0; index < nativeDisplays.length && displays.size() < options.maxDisplays(); index++) {
            Display display = nativeDisplays[index];
            DisplayMetrics metrics = new DisplayMetrics();
            display.getRealMetrics(metrics);
            if (metrics.widthPixels <= 0 || metrics.heightPixels <= 0) continue;
            displays.add(new DisplayInfo(
                    opaque("display", Integer.toString(display.getDisplayId())),
                    new SurfaceBounds(0, 0, metrics.widthPixels, metrics.heightPixels),
                    metrics.density, display.getDisplayId() == Display.DEFAULT_DISPLAY));
        }
        displays.sort(Comparator.comparing(DisplayInfo::id));

        List<WindowInfo> windows = new ArrayList<>();
        if (activity != null) {
            Window window = activity.getWindow();
            View decor = window == null ? null : window.getDecorView();
            if (decor != null && decor.getVisibility() == View.VISIBLE && decor.getWidth() > 0 && decor.getHeight() > 0) {
                int[] location = new int[2];
                decor.getLocationOnScreen(location);
                int pid = Process.myPid();
                windows.add(new WindowInfo(
                        opaque("window", Integer.toString(pid), processStartId, Integer.toString(System.identityHashCode(window))),
                        pid, processStartId,
                        new SurfaceBounds(location[0], location[1], decor.getWidth(), decor.getHeight()), true));
            }
        }
        return new SurfaceSnapshot(
                "android", "screen-pixels", now, displays, windows,
                new SurfaceCollectionStatus(displaysLimited ? "partial" : "complete", displaysLimited ? "display limit reached" : null, now),
                new SurfaceCollectionStatus("complete", null, now));
    }

    private static String timestamp() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date());
    }

    private static String opaque(String kind, String... values) {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            digest.update(kind.getBytes(StandardCharsets.UTF_8));
            for (String value : values) {
                digest.update((byte) 0);
                digest.update(value.getBytes(StandardCharsets.UTF_8));
            }
            byte[] bytes = digest.digest();
            StringBuilder result = new StringBuilder(kind).append(':');
            for (int index = 0; index < 12; index++) result.append(String.format(Locale.ROOT, "%02x", bytes[index] & 0xff));
            return result.toString();
        } catch (NoSuchAlgorithmException exception) {
            throw new IllegalStateException("SHA-256 is unavailable", exception);
        }
    }
}
