package dev.ext.consumer;

import static org.junit.Assert.assertTrue;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import androidx.test.core.app.ActivityScenario;
import androidx.test.platform.app.InstrumentationRegistry;
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
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.Test;

public class ConsumerTest {
    private final Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();

    @Test
    public void guestRoundTripThroughTheJniEngine() throws Exception {
        String reply = Roundtrips.guest();
        assertTrue(reply, reply.contains("\"n\":1.10") && reply.contains("héllo"));
        assertTrue(reply, reply.contains("\"code\":\"busy\""));
    }

    @Test
    public void webViewHostRoundTrip() throws Exception {
        String reply = Roundtrips.webView(context);
        assertTrue(reply, reply.contains("12345678901234567890"));
    }

    @Test
    public void webRulesAnswerAsGoDoesUnderArtAndR8() throws Exception {
        java.util.List<String> failures = WebRoundtrips.run();
        assertTrue(failures.toString(), failures.isEmpty());
    }

    @Test
    public void nativeClipboardRoundTrip() {
        AtomicReference<Throwable> failure = new AtomicReference<>();
        try (ActivityScenario<ClipboardActivity> scenario = ActivityScenario.launch(ClipboardActivity.class)) {
            scenario.onActivity(activity -> {
                try (SystemClipboard clipboard = new SystemClipboard(activity,
                        new ClipboardPolicy(1024, Collections.singletonList(SystemClipboard.TEXT), true, true))) {
                    String initial = clipboard.currentGeneration();
                    ClipboardItem item = new ClipboardItem(Collections.singletonList(
                            new ClipboardRepresentation(SystemClipboard.TEXT,
                                    "ext clipboard".getBytes(StandardCharsets.UTF_8))));
                    String written = clipboard.write(initial, Collections.singletonList(item)).generation;
                    ClipboardSnapshot snapshot = clipboard.read();
                    assertTrue(snapshot.generation, snapshot.generation.equals(written));
                    assertTrue(new String(snapshot.items.get(0).representations.get(0).data,
                            StandardCharsets.UTF_8).equals("ext clipboard"));
                    try {
                        clipboard.write(initial, Collections.singletonList(item));
                        throw new AssertionError("stale generation was accepted");
                    } catch (io.github.webong.ext.clipboard.ClipboardException expected) {
                        assertTrue(expected.code, expected.code.equals("changed"));
                    }
                    clipboard.clear(snapshot.generation);
                    assertTrue(clipboard.read().items.isEmpty());
                } catch (Throwable error) {
                    failure.set(error);
                }
            });
        }
        if (failure.get() != null) throw new AssertionError("native clipboard round trip", failure.get());
    }

    @Test
    public void nativeSystemObservationIsAppScoped() {
        AtomicReference<Throwable> failure = new AtomicReference<>();
        try (ActivityScenario<ClipboardActivity> scenario = ActivityScenario.launch(ClipboardActivity.class)) {
            scenario.onActivity(activity -> {
                try {
                    SurfaceSnapshot snapshot = new SystemSurfaceObserver(activity).observe(activity);
                    assertTrue(snapshot.displayStatus().state(), !snapshot.displayStatus().state().isEmpty());
                    assertTrue("expected an app-visible display", !snapshot.displays().isEmpty());
                    snapshot.displays().forEach(display -> {
                        assertTrue(display.id(), display.id().startsWith("display:"));
                        assertTrue("display width", display.bounds().width() > 0);
                        assertTrue("display height", display.bounds().height() > 0);
                    });
                    snapshot.windows().forEach(window -> {
                        assertTrue(window.id(), window.id().startsWith("window:"));
                        assertTrue(window.processStartId(), !window.processStartId().isEmpty());
                    });
                } catch (Throwable error) {
                    failure.set(error);
                }
            });
        }
        if (failure.get() != null) throw new AssertionError("native system observation", failure.get());
    }

	@Test
	public void nativeAccessibilityPermissionsAndNetworkRemainObservations() {
		AtomicReference<Throwable> failure = new AtomicReference<>();
		try (ActivityScenario<ClipboardActivity> scenario = ActivityScenario.launch(ClipboardActivity.class)) {
			scenario.onActivity(activity -> {
				try {
					AccessibilitySnapshot accessibility = new SystemAccessibilityObserver().observe(activity);
					assertTrue(accessibility.status().state(), !accessibility.status().state().isEmpty());
					accessibility.elements().forEach(element -> {
						assertTrue(element.id(), element.id().startsWith("accessibility:"));
						assertTrue("text is opt-in", element.label() == null && element.value() == null);
					});
					PermissionSnapshot permissions = SystemPermissionObserver.observe();
					assertTrue(permissions.accessibility().state(), !permissions.accessibility().state().isEmpty());
					assertTrue(permissions.screenRecording().state(), !permissions.screenRecording().state().isEmpty());
					NetworkSnapshot network = new SystemNetworkObserver().observe(activity);
					assertTrue(network.interfaceStatus().state(), !network.interfaceStatus().state().isEmpty());
					assertTrue(network.routeStatus().state(), !network.routeStatus().state().isEmpty());
					assertTrue(network.resolverStatus().state(), !network.resolverStatus().state().isEmpty());
				} catch (Throwable error) {
					failure.set(error);
				}
			});
		}
		if (failure.get() != null) throw new AssertionError("native observation", failure.get());
	}

    @Test
    public void packagedNativeLibrariesMatchTheDeviceAbi() {
        ApplicationInfo info = context.getApplicationInfo();
        File libraries = new File(info.nativeLibraryDir);
        String[] names = libraries.list();
        assertTrue("no native libraries in " + libraries, names != null && names.length > 0);
        boolean found = false;
        for (String name : names) {
            found |= name.equals("libextjni.so");
        }
        assertTrue("libextjni.so is not in " + libraries, found);
    }
}
