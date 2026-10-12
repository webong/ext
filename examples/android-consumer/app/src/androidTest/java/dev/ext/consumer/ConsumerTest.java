package dev.ext.consumer;

import static org.junit.Assert.assertTrue;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import androidx.test.platform.app.InstrumentationRegistry;
import java.io.File;
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
