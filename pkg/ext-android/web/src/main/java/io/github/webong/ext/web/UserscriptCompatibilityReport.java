package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/** Import-time metadata assessment. Inspection never installs or executes source. */
public final class UserscriptCompatibilityReport {
    public final String name, runAt, world;
    public final List<String> grants, matches, excludeMatches, includes, excludes, blockers, notes;
    public final boolean allFrames;

    UserscriptCompatibilityReport(String name, List<String> grants, List<String> matches,
            List<String> excludeMatches, List<String> includes, List<String> excludes,
            String runAt, String world, boolean allFrames, List<String> blockers, List<String> notes) {
        this.name = name; this.runAt = runAt; this.world = world; this.allFrames = allFrames;
        this.grants = immutable(grants); this.matches = immutable(matches);
        this.excludeMatches = immutable(excludeMatches); this.includes = immutable(includes);
        this.excludes = immutable(excludes); this.blockers = immutable(blockers); this.notes = immutable(notes);
    }

    private static List<String> immutable(List<String> values) {
        return Collections.unmodifiableList(new ArrayList<>(values));
    }

    public boolean runnable() { return blockers.isEmpty(); }

    @Override public boolean equals(Object other) {
        if (!(other instanceof UserscriptCompatibilityReport)) return false;
        UserscriptCompatibilityReport value = (UserscriptCompatibilityReport) other;
        return name.equals(value.name) && grants.equals(value.grants) && matches.equals(value.matches)
                && excludeMatches.equals(value.excludeMatches) && includes.equals(value.includes)
                && excludes.equals(value.excludes) && runAt.equals(value.runAt) && world.equals(value.world)
                && allFrames == value.allFrames && blockers.equals(value.blockers) && notes.equals(value.notes);
    }

    @Override public int hashCode() {
        return Objects.hash(name, grants, matches, excludeMatches, includes, excludes, runAt, world,
                allFrames, blockers, notes);
    }
}
