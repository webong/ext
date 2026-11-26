package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/** One native WebView script execution path. This declaration grants no authority. */
public final class ScriptExecutionCapability {
    public final boolean supported, rejectsStaleNavigation;
    public final List<String> frames, runAt, worlds;

    public ScriptExecutionCapability(boolean supported, List<String> frames, List<String> runAt,
            List<String> worlds, boolean rejectsStaleNavigation) {
        this.supported = supported; this.rejectsStaleNavigation = rejectsStaleNavigation;
        this.frames = immutable(frames); this.runAt = immutable(runAt); this.worlds = immutable(worlds);
    }

    private static List<String> immutable(List<String> values) {
        return Collections.unmodifiableList(new ArrayList<>(values));
    }
}
