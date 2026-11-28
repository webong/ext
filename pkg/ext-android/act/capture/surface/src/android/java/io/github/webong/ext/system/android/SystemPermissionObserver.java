package io.github.webong.ext.system.android;

import io.github.webong.ext.system.PermissionInfo;
import io.github.webong.ext.system.PermissionSnapshot;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.TimeZone;

/** Reads capability state without launching a permission prompt or settings UI. */
public final class SystemPermissionObserver {
    private SystemPermissionObserver() {}

    public static PermissionSnapshot observe() {
        return new PermissionSnapshot("android", timestamp(),
                new PermissionInfo("not-required", "android.app-owned-ui", null),
                new PermissionInfo("not-determined", "android.media-projection",
                        "MediaProjection consent is determined when capture is requested"));
    }

    private static String timestamp() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date());
    }
}
