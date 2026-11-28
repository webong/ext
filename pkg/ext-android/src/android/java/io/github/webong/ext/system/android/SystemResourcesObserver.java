package io.github.webong.ext.system.android;

import android.content.Context;
import io.github.webong.ext.system.ContainerInfo;
import io.github.webong.ext.system.PowerInfo;
import io.github.webong.ext.system.ResourceCancellation;
import io.github.webong.ext.system.ResourceCollectionStatus;
import io.github.webong.ext.system.ResourceInfo;
import io.github.webong.ext.system.StorageInfo;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/** Binds the C ledger collector and decodes its shared resource wire record. */
// Loading the app's own native library is what this class is for; newer JDKs flag
// System.loadLibrary as restricted, which means nothing on Android.
@SuppressWarnings("restricted")
public final class SystemResourcesObserver {
    static { System.loadLibrary("ext_ledger"); }

    public SystemResourcesObserver(Context context) {
        if (context == null) throw new IllegalArgumentException("context is required");
    }

    private static native String collectNative();

    public ResourceInfo observe() { return observe(new ResourceCancellation()); }

    public ResourceInfo observe(ResourceCancellation cancellation) {
        if (cancellation == null) throw new IllegalArgumentException("cancellation is required");
        cancellation.check();
        String wire = collectNative();
        cancellation.check();
        try {
            return decode(new JSONObject(wire));
        } catch (JSONException error) {
            throw new IllegalStateException("ledger returned an invalid resource record", error);
        }
    }

    private static ResourceCollectionStatus status(JSONObject value) throws JSONException {
        return new ResourceCollectionStatus(value.getString("state"), value.optString("reason", null),
                value.optString("detail", null), value.optString("observed_at", null));
    }

    private static ResourceInfo decode(JSONObject value) throws JSONException {
        List<StorageInfo> storage = new ArrayList<>();
        JSONArray volumes = value.optJSONArray("storage");
        if (volumes != null) for (int index = 0; index < volumes.length(); index++) {
            JSONObject item = volumes.getJSONObject(index);
            storage.add(new StorageInfo(item.getString("device"), item.getString("mount_point"),
                    item.getLong("total_bytes"), item.getLong("available_bytes"),
                    item.optString("type", null), item.optBoolean("read_only", false)));
        }
        PowerInfo power = null;
        JSONObject battery = value.optJSONObject("power");
        if (battery != null) {
            power = new PowerInfo(battery.getString("state"), battery.getInt("percentage"),
                    battery.has("time_remaining_seconds") ? battery.getLong("time_remaining_seconds") : null);
        }
        JSONObject container = value.getJSONObject("container");
        List<String> evidence = new ArrayList<>();
        JSONArray names = container.getJSONArray("evidence");
        for (int index = 0; index < names.length(); index++) evidence.add(names.getString(index));
        return new ResourceInfo(value.getString("os"), value.getString("architecture"),
                value.getString("observed_at"), value.getInt("logical_cpus"), value.getDouble("cpu_capacity"),
                status(value.getJSONObject("cpu_status")), value.getLong("memory_bytes"),
                value.getLong("memory_capacity_bytes"), status(value.getJSONObject("memory_status")),
                storage, power, new ContainerInfo(container.getBoolean("detected"), container.getString("state"), evidence),
                value.has("uptime_seconds") ? value.getDouble("uptime_seconds") : null,
                value.optString("boot_time", null), status(value.getJSONObject("uptime_status")));
    }
}
