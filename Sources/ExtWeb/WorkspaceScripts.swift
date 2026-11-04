import Foundation

/// Prepares a complete registration set before a native workspace changes its scripts.
/// Records must belong to the caller-selected target and have reviewed, matching revisions.
public enum WorkspaceScripts {
    public static func prepare(_ records: [UserscriptRecord], target: String) throws -> [String] {
        guard records.count <= 512, records.reduce(0, { $0 + $1.source.utf8.count }) <= 8 << 20 else {
            throw WorkspaceScriptError.exceedsLimit
        }
        var ids = Set<String>()
        var sources: [String] = []
        for record in records {
            try Userscript.validate(record)
            guard record.target == target, ids.insert(record.id).inserted else {
                throw WorkspaceScriptError.invalidTargetOrDuplicateID
            }
            if record.enabled {
                sources.append(try Userscript.replaySource(id: record.id, revision: record.revision,
                    source: record.source, matches: record.matches, excludes: record.excludeMatches))
            }
        }
        return sources
    }
}

public enum WorkspaceScriptError: Error {
    case exceedsLimit
    case invalidTargetOrDuplicateID
}
