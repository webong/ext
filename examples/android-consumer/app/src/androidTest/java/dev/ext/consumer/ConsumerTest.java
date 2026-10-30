package dev.ext.consumer;

import static org.junit.Assert.assertTrue;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import androidx.test.core.app.ActivityScenario;
import androidx.test.platform.app.InstrumentationRegistry;
import java.io.File;
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
    public void nativeClipboardRoundTrip() { withActivity(ShellRoundtrips::clipboard); }

    @Test
    public void nativeSystemObservationIsAppScoped() { withActivity(ShellRoundtrips::surfaces); }

    @Test
    public void nativeAccessibilityPermissionsAndNetworkRemainObservations() {
        withActivity(ShellRoundtrips::observations);
    }

    private interface ActivityCheck { void accept(ClipboardActivity activity) throws Exception; }

    private void withActivity(ActivityCheck action) {
        AtomicReference<Throwable> failure = new AtomicReference<>();
        try (ActivityScenario<ClipboardActivity> scenario = ActivityScenario.launch(ClipboardActivity.class)) {
            scenario.onActivity(activity -> {
                try { action.accept(activity); }
                catch (Throwable error) { failure.set(error); }
            });
        }
        if (failure.get() != null) throw new AssertionError("native shell round trip", failure.get());
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
