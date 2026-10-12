package dev.ext.consumer;

import static org.junit.Assert.assertTrue;

import android.content.Context;
import androidx.test.platform.app.InstrumentationRegistry;
import java.io.File;
import org.junit.Test;

public class WamrConsumerTest {
    private final Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();

    @Test
    public void wamrHostRoundTrip() throws Exception {
        String reply = WamrRoundtrip.echo(context);
        assertTrue(reply, reply.contains("12345678901234567890"));
    }

    @Test
    public void wamrLibraryIsPackaged() {
        String[] names = new File(context.getApplicationInfo().nativeLibraryDir).list();
        boolean found = false;
        for (String name : names == null ? new String[0] : names) {
            found |= name.equals("libextwamrjni.so");
        }
        assertTrue("libextwamrjni.so is not packaged", found);
    }
}
