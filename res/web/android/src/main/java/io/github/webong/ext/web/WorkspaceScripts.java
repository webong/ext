package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/** Validates an entire reviewed registration set before any native state changes. */
public final class WorkspaceScripts {
    private WorkspaceScripts() {}

    public static List<String> prepare(List<UserscriptRecord> records, String target) throws UserscriptException {
        Set<String> ids = new HashSet<>();
        List<String> sources = new ArrayList<>();
        for (UserscriptRecord record : records) {
            Userscript.validate(record);
            if (!record.target.equals(target) || !ids.add(record.id)) {
                throw new IllegalArgumentException("userscript target mismatch or duplicate id");
            }
            if (record.enabled) {
                sources.add(Userscript.replaySource(record.id, record.revision, record.source,
                        record.matches, record.excludeMatches));
            }
        }
        return sources;
    }
}
