package io.github.webong.ext.system.android;

import android.app.Activity;
import android.os.Looper;
import android.os.Process;
import android.view.View;
import android.view.ViewGroup;
import android.view.Window;
import android.widget.TextView;
import io.github.webong.ext.system.AccessibilityElement;
import io.github.webong.ext.system.AccessibilityOptions;
import io.github.webong.ext.system.AccessibilitySnapshot;
import io.github.webong.ext.system.SurfaceBounds;
import io.github.webong.ext.system.SurfaceCollectionStatus;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.text.SimpleDateFormat;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Date;
import java.util.Deque;
import java.util.List;
import java.util.Locale;
import java.util.TimeZone;

/** Read-only observation of an Activity's own view tree. */
public final class SystemAccessibilityObserver {
    private static final String PROCESS_START_ID = Long.toString(Process.getStartElapsedRealtime());
    private final AccessibilityOptions options;

    public SystemAccessibilityObserver(AccessibilityOptions options) {
        this.options = options;
    }

    public SystemAccessibilityObserver() { this(new AccessibilityOptions()); }

    public AccessibilitySnapshot observe(Activity activity) {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            throw new IllegalStateException("system accessibility observation must run on the main thread");
        }
        String now = timestamp();
        List<AccessibilityElement> result = new ArrayList<>();
        if (activity == null) {
            return new AccessibilitySnapshot("android", now,
                    new SurfaceCollectionStatus("complete", null, now), result);
        }
        Window window = activity.getWindow();
        View root = window == null ? null : window.getDecorView();
        if (root == null) {
            return new AccessibilitySnapshot("android", now,
                    new SurfaceCollectionStatus("unavailable", "activity window is unavailable", now), result);
        }
        Deque<Node> queue = new ArrayDeque<>();
        queue.add(new Node(root, null, "0", 0));
        boolean truncated = false;
        int pid = Process.myPid();
        while (!queue.isEmpty()) {
            Node node = queue.removeFirst();
            if (result.size() >= options.maxElements()) { truncated = true; continue; }
            String id = opaque(pid, PROCESS_START_ID, node.path);
            int[] location = new int[2];
            node.view.getLocationOnScreen(location);
            SurfaceBounds bounds = node.view.getWidth() > 0 && node.view.getHeight() > 0
                    ? new SurfaceBounds(location[0], location[1], node.view.getWidth(), node.view.getHeight()) : null;
            String identifier = resourceName(node.view);
            String label = options.includeText() && node.view.getContentDescription() != null
                    ? node.view.getContentDescription().toString() : null;
            String value = options.includeText() && node.view instanceof TextView
                    ? ((TextView) node.view).getText().toString() : null;
            List<String> actions = node.view.isClickable()
                    ? Collections.singletonList("activate") : Collections.emptyList();
            result.add(new AccessibilityElement(id, node.parentId, pid, PROCESS_START_ID,
                    node.view.getClass().getSimpleName(), identifier, label, value, bounds, actions,
                    node.view.isEnabled(), node.view.isFocused()));
            if (!(node.view instanceof ViewGroup)) continue;
            ViewGroup group = (ViewGroup) node.view;
            if (node.depth >= options.maxDepth()) {
                if (group.getChildCount() > 0) truncated = true;
                continue;
            }
            for (int index = 0; index < group.getChildCount(); index++) {
                queue.add(new Node(group.getChildAt(index), id, node.path + "/" + index, node.depth + 1));
            }
        }
        result.sort((left, right) -> left.id().compareTo(right.id()));
        return new AccessibilitySnapshot("android", now,
                new SurfaceCollectionStatus(truncated ? "partial" : "complete",
                        truncated ? "accessibility element or depth limit reached" : null, now), result);
    }

    private static String resourceName(View view) {
        if (view.getId() == View.NO_ID) return null;
        try { return view.getResources().getResourceName(view.getId()); }
        catch (RuntimeException ignored) { return null; }
    }

    private static String timestamp() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date());
    }

    private static String opaque(int pid, String generation, String path) {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            digest.update("accessibility".getBytes(StandardCharsets.UTF_8));
            for (String value : new String[]{Integer.toString(pid), generation, path}) {
                digest.update((byte) 0); digest.update(value.getBytes(StandardCharsets.UTF_8));
            }
            byte[] bytes = digest.digest();
            StringBuilder result = new StringBuilder("accessibility:");
            for (int index = 0; index < 12; index++) result.append(String.format(Locale.ROOT, "%02x", bytes[index] & 0xff));
            return result.toString();
        } catch (NoSuchAlgorithmException exception) {
            throw new IllegalStateException("SHA-256 is unavailable", exception);
        }
    }

    private static final class Node {
        final View view; final String parentId; final String path; final int depth;
        Node(View view, String parentId, String path, int depth) {
            this.view = view; this.parentId = parentId; this.path = path; this.depth = depth;
        }
    }
}
