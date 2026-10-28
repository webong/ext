package io.github.webong.ext.system.android;

import android.app.ActivityManager;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Environment;
import android.os.StatFs;
import io.github.webong.ext.system.ContainerInfo;
import io.github.webong.ext.system.PowerInfo;
import io.github.webong.ext.system.ResourceCollectionStatus;
import io.github.webong.ext.system.ResourceInfo;
import io.github.webong.ext.system.StorageInfo;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.Locale;
import java.util.TimeZone;

/** Reads app-visible Android host resources: CPU, memory, storage, and battery. */
public final class SystemResourcesObserver {
    private final Context context;

    public SystemResourcesObserver(Context context) {
        if (context == null) throw new IllegalArgumentException("context is required");
        this.context = context.getApplicationContext();
    }

    public ResourceInfo observe() {
        String now = timestamp();
        int logicalCPUs = Runtime.getRuntime().availableProcessors();
        if (logicalCPUs < 1) logicalCPUs = 1;

        long[] memory = platformMemory();
        List<StorageInfo> storage = platformStorage();
        PowerInfo power = platformPower();
        ContainerInfo container = new ContainerInfo(false, "unsupported", new ArrayList<>());
        Double uptime = platformUptime();

        return new ResourceInfo(
                "android", architecture(), now,
                logicalCPUs, (double) logicalCPUs,
                new ResourceCollectionStatus("complete", "logical CPU count", now),
                memory[0], memory[1],
                new ResourceCollectionStatus("complete", "physical memory capacity", now),
                storage, power, container, uptime, null,
                new ResourceCollectionStatus(uptime == null ? "unsupported" : "complete",
                        uptime == null ? "boot time is unavailable" : null, now));
    }

    private String architecture() {
        String[] abis = Build.SUPPORTED_ABIS;
        if (abis != null && abis.length > 0) return abis[0];
        return "unknown";
    }

    private long[] platformMemory() {
        ActivityManager manager = (ActivityManager) context.getSystemService(Context.ACTIVITY_SERVICE);
        if (manager == null) return new long[]{0, 0};
        ActivityManager.MemoryInfo info = new ActivityManager.MemoryInfo();
        manager.getMemoryInfo(info);
        // totalMem is capacity; used is total minus available.
        long capacity = info.totalMem;
        long used = capacity - info.availMem;
        if (used < 0) used = 0;
        return new long[]{used, capacity};
    }

    private List<StorageInfo> platformStorage() {
        List<StorageInfo> result = new ArrayList<>();
        try {
            StatFs stat = new StatFs(Environment.getDataDirectory().getPath());
            long total = stat.getBlockCountLong() * stat.getBlockSizeLong();
            long available = stat.getAvailableBlocksLong() * stat.getBlockSizeLong();
            result.add(new StorageInfo("local", Environment.getDataDirectory().getPath(), total, available, "ext4", false));
        } catch (Exception exception) {
            // Storage observation is best-effort from a sandboxed process.
        }
        return result;
    }

    private PowerInfo platformPower() {
        IntentFilter filter = new IntentFilter(Intent.ACTION_BATTERY_CHANGED);
        Intent intent = context.registerReceiver(null, filter);
        if (intent == null) return null;
        int level = intent.getIntExtra(BatteryManager.EXTRA_LEVEL, -1);
        int scale = intent.getIntExtra(BatteryManager.EXTRA_SCALE, -1);
        if (level < 0 || scale <= 0) return null;
        int percentage = (int) ((level * 100L) / scale);
        int status = intent.getIntExtra(BatteryManager.EXTRA_STATUS, BatteryManager.BATTERY_STATUS_UNKNOWN);
        String state;
        switch (status) {
            case BatteryManager.BATTERY_STATUS_CHARGING: state = "charging"; break;
            case BatteryManager.BATTERY_STATUS_FULL: state = "charged"; break;
            case BatteryManager.BATTERY_STATUS_DISCHARGING: state = "discharging"; break;
            default: state = "unknown";
        }
        return new PowerInfo(state, Math.max(0, Math.min(100, percentage)), null);
    }

    private Double platformUptime() {
        // elapsedRealtime is time since boot including deep sleep.
        return (double) android.os.SystemClock.elapsedRealtime() / 1000.0;
    }

    private String timestamp() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date());
    }
}
