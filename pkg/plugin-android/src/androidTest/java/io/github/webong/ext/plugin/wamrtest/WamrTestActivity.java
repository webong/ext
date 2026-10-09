package io.github.webong.ext.plugin.wamrtest;

import android.app.Activity;
import android.os.Bundle;
import android.util.Log;
import io.github.webong.ext.plugin.WamrPluginHostTests;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;

/**
 * Runs the ext.conformance/v1 reactor (assets/reactor.wasm) on the in-process WAMR host, the
 * same checks as the desktop suite, and reports to logcat under the tag EXTTEST.
 * scripts/plugin-android-wamr.sh runs it on an emulator.
 */
public final class WamrTestActivity extends Activity {
    private static final String TAG = "EXTTEST";

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        new Thread(this::runAll, "ext-tests").start();
    }

    private void runAll() {
        int failed = 1;
        int total = 0;
        try (InputStream in = getAssets().open("reactor.wasm")) {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buffer = new byte[65536];
            for (int n; (n = in.read(buffer)) > 0; ) {
                out.write(buffer, 0, n);
            }
            int[] counts = new int[1];
            failed = WamrPluginHostTests.runAll(out.toByteArray(), line -> {
                Log.i(TAG, line);
                if (line.startsWith("ok ")) {
                    counts[0]++;
                }
            });
            total = counts[0];
        } catch (Throwable t) {
            Log.e(TAG, "the WAMR host suite did not run: " + t);
        }
        Log.i(TAG, "DONE passed=" + total + " failed=" + failed);
        runOnUiThread(this::finish);
    }
}
