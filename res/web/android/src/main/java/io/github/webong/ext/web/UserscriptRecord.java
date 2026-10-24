package io.github.webong.ext.web;

import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/**
 * A userscript the caller has reviewed, as {@code res/web/userscript} stores it
 * (ext.userscript/v1alpha1).
 *
 * <p>{@link #matches} and {@link #excludeMatches} may be null because the revision digest tells
 * null from an empty list.
 */
public final class UserscriptRecord {
    public final String target;
    public final String id;
    public final String name;
    public final String revision;
    public final List<String> matches;
    public final List<String> excludeMatches;
    public final boolean enabled;
    public final String source;

    public UserscriptRecord(String target, String id, String name, String revision, List<String> matches,
            List<String> excludeMatches, boolean enabled, String source) {
        this.target = Objects.requireNonNull(target);
        this.id = Objects.requireNonNull(id);
        this.name = Objects.requireNonNull(name);
        this.revision = Objects.requireNonNull(revision);
        this.matches = matches == null ? null : Collections.unmodifiableList(new java.util.ArrayList<>(matches));
        this.excludeMatches = excludeMatches == null ? null : Collections.unmodifiableList(new java.util.ArrayList<>(excludeMatches));
        this.enabled = enabled;
        this.source = Objects.requireNonNull(source);
    }

    public UserscriptRecord(String target, String id, String name, List<String> matches, List<String> excludeMatches,
            String source) {
        this(target, id, name, "", matches, excludeMatches, false, source);
    }

    public UserscriptRecord withRevision(String newRevision) {
        return new UserscriptRecord(target, id, name, newRevision, matches, excludeMatches, enabled, source);
    }

    /** The same record with the revision {@link Userscript#revision} computes for it. */
    public UserscriptRecord signed() {
        return withRevision(Userscript.revision(this));
    }

    /** Reads the stored form. A missing or null member is the zero value; an absent list stays null. */
    public static UserscriptRecord fromJson(String json) {
        Map<String, Object> o = Json.object(Json.parse(json), "a userscript record");
        return new UserscriptRecord(Json.string(o, "target"), Json.string(o, "id"), Json.string(o, "name"),
                Json.string(o, "revision"), Json.strings(o, "matches"), Json.strings(o, "excludeMatches"),
                Json.bool(o, "enabled"), Json.string(o, "source"));
    }

    /** The stored form: every member is written, and an absent list is {@code null}, as Go's encoder does. */
    public String toJson() {
        StringBuilder out = new StringBuilder("{\"target\":");
        GoText.jsonQuote(out, target).append(",\"id\":");
        GoText.jsonQuote(out, id).append(",\"name\":");
        GoText.jsonQuote(out, name).append(",\"revision\":");
        GoText.jsonQuote(out, revision).append(",\"matches\":");
        GoText.jsonArray(out, matches).append(",\"excludeMatches\":");
        GoText.jsonArray(out, excludeMatches).append(",\"enabled\":").append(enabled).append(",\"source\":");
        GoText.jsonQuote(out, source);
        return out.append('}').toString();
    }

    @Override
    public boolean equals(Object other) {
        if (!(other instanceof UserscriptRecord)) {
            return false;
        }
        UserscriptRecord r = (UserscriptRecord) other;
        return target.equals(r.target) && id.equals(r.id) && name.equals(r.name) && revision.equals(r.revision)
                && Objects.equals(matches, r.matches) && Objects.equals(excludeMatches, r.excludeMatches)
                && enabled == r.enabled && source.equals(r.source);
    }

    @Override
    public int hashCode() {
        return Objects.hash(target, id, name, revision, matches, excludeMatches, enabled, source);
    }
}
