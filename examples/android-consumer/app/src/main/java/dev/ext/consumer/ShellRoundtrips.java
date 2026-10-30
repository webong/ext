package dev.ext.consumer;

import android.app.Activity;
import io.github.webong.ext.clipboard.ClipboardItem;
import io.github.webong.ext.clipboard.ClipboardPolicy;
import io.github.webong.ext.clipboard.ClipboardRepresentation;
import io.github.webong.ext.clipboard.ClipboardSnapshot;
import io.github.webong.ext.clipboard.android.SystemClipboard;
import io.github.webong.ext.system.SurfaceSnapshot;
import io.github.webong.ext.system.AccessibilitySnapshot;
import io.github.webong.ext.system.PermissionSnapshot;
import io.github.webong.ext.system.android.SystemAccessibilityObserver;
import io.github.webong.ext.system.android.SystemPermissionObserver;
import io.github.webong.ext.system.android.SystemSurfaceObserver;
import io.github.webong.ext.network.NetworkSnapshot;
import io.github.webong.ext.network.android.SystemNetworkObserver;
import java.nio.charset.StandardCharsets;
import java.util.Collections;

// These calls live in the application APK so R8 sees real SDK usage. The test
// APK invokes only this app-owned entry point, never classes R8 may remove.
public final class ShellRoundtrips {
    private ShellRoundtrips() {}

    public static void clipboard(Activity activity) throws Exception {
        try (SystemClipboard clipboard = new SystemClipboard(activity,
                new ClipboardPolicy(1024, Collections.singletonList(SystemClipboard.TEXT), true, true))) {
            String initial = clipboard.currentGeneration();
            ClipboardItem item = new ClipboardItem(Collections.singletonList(
                    new ClipboardRepresentation(SystemClipboard.TEXT,
                            "ext clipboard".getBytes(StandardCharsets.UTF_8))));
            String written = clipboard.write(initial, Collections.singletonList(item)).generation;
            ClipboardSnapshot snapshot = clipboard.read();
            check(snapshot.generation, snapshot.generation.equals(written));
            check(new String(snapshot.items.get(0).representations.get(0).data,
                    StandardCharsets.UTF_8).equals("ext clipboard"));
            try {
                clipboard.write(initial, Collections.singletonList(item));
                throw new AssertionError("stale generation was accepted");
            } catch (io.github.webong.ext.clipboard.ClipboardException expected) {
                check(expected.code, expected.code.equals("changed"));
            }
            clipboard.clear(snapshot.generation);
            check(clipboard.read().items.isEmpty());
        }
    }

    public static void surfaces(Activity activity) {
        SurfaceSnapshot snapshot = new SystemSurfaceObserver(activity).observe(activity);
        check(snapshot.displayStatus().state(), !snapshot.displayStatus().state().isEmpty());
        check("expected an app-visible display", !snapshot.displays().isEmpty());
        snapshot.displays().forEach(display -> {
            check(display.id(), display.id().startsWith("display:"));
            check("display width", display.bounds().width() > 0);
            check("display height", display.bounds().height() > 0);
        });
        snapshot.windows().forEach(window -> {
            check(window.id(), window.id().startsWith("window:"));
            check(window.processStartId(), !window.processStartId().isEmpty());
        });
    }

    public static void observations(Activity activity) {
        AccessibilitySnapshot accessibility = new SystemAccessibilityObserver().observe(activity);
        check(accessibility.status().state(), !accessibility.status().state().isEmpty());
        accessibility.elements().forEach(element -> {
            check(element.id(), element.id().startsWith("accessibility:"));
            check("text is opt-in", element.label() == null && element.value() == null);
        });
        PermissionSnapshot permissions = SystemPermissionObserver.observe();
        check(permissions.accessibility().state(), !permissions.accessibility().state().isEmpty());
        check(permissions.screenRecording().state(), !permissions.screenRecording().state().isEmpty());
        NetworkSnapshot network = new SystemNetworkObserver().observe(activity);
        check(network.interfaceStatus().state(), !network.interfaceStatus().state().isEmpty());
        check(network.routeStatus().state(), !network.routeStatus().state().isEmpty());
        check(network.resolverStatus().state(), !network.resolverStatus().state().isEmpty());

    }
    private static void check(boolean value) { check("shell observation", value); }
    private static void check(String message, boolean value) {
        if (!value) throw new AssertionError(message);
    }
}
