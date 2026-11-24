import Foundation

/// Opt-in embedded WebView profiles. Legacy userscript storage and its
/// revision format remain available through Userscript and WorkspaceScripts.
public enum WebViewUserscripts {
    private static let domain = "ext.webview-userscript/v1\n"

    public enum Timing: String, Sendable, Codable {
        case documentStart = "document-start"
        case documentEnd = "document-end"
        case documentIdle = "document-idle"
    }

    public enum Frames: String, Sendable, Codable { case main, all }

    /// An exact combination the native host can register in the page world.
    public struct Profile: Equatable, Sendable {
        public let timing: Timing
        public let frames: Frames

        public init(timing: Timing, frames: Frames) {
            self.timing = timing
            self.frames = frames
        }
    }

    public struct Registration: Sendable {
        public let profile: Profile
        public let source: String
    }

    /// The digest a consumer records after reviewing the v1 source and scope.
    /// The source contains timing, frame, world, and URL glob metadata.
    public static func revision(of record: UserscriptRecord) -> String {
        Userscript.revision(of: record, domain: domain)
    }

    /// Checks a whole batch before native state changes. Only active records
    /// require a native hook. A matching revision is evidence of content
    /// identity. The caller supplies approval and the selected view.
    public static func prepare(_ records: [UserscriptRecord], target: String,
                               supported: [Profile]) throws -> [Registration] {
        guard records.count <= 512, records.reduce(0, { $0 + $1.source.utf8.count }) <= 8 << 20 else {
            throw WorkspaceScriptError.exceedsLimit
        }
        var ids = Set<String>()
        var registrations: [Registration] = []
        for record in records {
            try Userscript.validateIdentity(record, domain: domain)
            guard record.target == target, ids.insert(record.id).inserted else {
                throw WorkspaceScriptError.invalidTargetOrDuplicateID
            }
            let metadata = Userscript.compatibility(of: record.source, profiled: true)
            if let blocker = metadata.blockers.first { throw reject(blocker, "unsupported WebView userscript metadata") }
            guard let timing = Timing(rawValue: metadata.runAt) else {
                throw reject("unsupported_run_at", "WebView userscripts require an explicit document timing")
            }
            guard metadata.matches.sorted() == (record.matches ?? []).sorted(),
                  metadata.excludeMatches.sorted() == (record.excludeMatches ?? []).sorted() else {
                throw reject("match_metadata_mismatch", "WebView userscript record scope disagrees with its header")
            }
            try Userscript.validateMatchPatterns(record, allowEmpty: !metadata.includes.isEmpty)
            try validateGlobs(metadata.includes, metadata.excludes)
            if !record.enabled { continue }
            let profile = Profile(timing: timing, frames: metadata.allFrames ? .all : .main)
            guard supported.contains(profile) else {
                throw reject("unsupported_profile", "the selected WebView cannot register this timing and frame scope")
            }
            registrations.append(Registration(profile: profile, source: replay(record, metadata: metadata)))
        }
        return registrations
    }

    private static func validateGlobs(_ includes: [String], _ excludes: [String]) throws {
        guard includes.count <= 128, excludes.count <= 128 else {
            throw reject("invalid_url_glob", "too many WebView userscript URL globs")
        }
        var seen = Set<String>()
        for pattern in includes + excludes {
            guard pattern.utf8.count <= 512, !pattern.contains("#"), !pattern.contains("\\"),
                  let range = pattern.range(of: #"^https?://[a-z0-9-]+(?:\.[a-z0-9-]+)*/[!-~]*$"#,
                                            options: .regularExpression),
                  range == pattern.startIndex..<pattern.endIndex, seen.insert(pattern).inserted else {
                throw reject("invalid_url_glob", "expected an HTTP(S) URL glob with a literal lowercase host")
            }
        }
    }

    private static func reject(_ code: String, _ message: String) -> UserscriptError {
        UserscriptError(code: code, message: message)
    }

    private static func replay(_ record: UserscriptRecord, metadata: UserscriptCompatibilityReport) -> String {
        let values = [
            GoJSON.quote(record.id), GoJSON.quote(record.revision), GoJSON.quote(record.source),
            GoJSON.array(record.matches ?? []), GoJSON.array(record.excludeMatches ?? []),
            GoJSON.array(metadata.includes), GoJSON.array(metadata.excludes), metadata.allFrames ? "false" : "true",
        ]
        return "(() => {\n const args = ["
            + values.joined(separator: ",") + "];\n" + scope + Userscript.replayBody
    }

    // Use native Location fields and literal matching before touching page-overridable builtins.
    private static let scope = """
 const path = location.pathname + location.search;
 const address = location.origin + path;
 if ((args[7] && window.top !== window) || address.length > 8192) return;
 const equalAt = (a, start, b, offset, count) => {
  if (start < 0 || start + count > a.length) return false;
  for (let i = 0; i < count; i++) if (a[start + i] !== b[offset + i]) return false;
  return true;
 };
 const glob = (pattern, value, p) => {
  let v = 0, star = -1, next = 0;
  while (v < value.length) {
   if (p < pattern.length && pattern[p] === '*') { star = p++; next = v; }
   else if (p < pattern.length && pattern[p] === value[v]) { p++; v++; }
   else if (star >= 0) { p = star + 1; v = ++next; }
   else return false;
  }
  while (p < pattern.length && pattern[p] === '*') p++;
  return p === pattern.length;
 };
 const match = pattern => {
  let colon = 0;
  while (pattern[colon] !== ':') colon++;
  const protocol = location.protocol, hostname = location.hostname;
  if (pattern[0] === '*') {
   if (protocol !== 'http:' && protocol !== 'https:') return false;
  } else if (protocol.length !== colon + 1 || !equalAt(protocol, 0, pattern, 0, colon + 1)) return false;
  const start = colon + 3;
  let slash = start;
  while (pattern[slash] !== '/') slash++;
  const count = slash - start;
  if (pattern[start] === '*' && pattern[start + 1] === '.') {
   const length = count - 2, offset = hostname.length - length;
   if ((offset !== 0 && hostname[offset - 1] !== '.') || !equalAt(hostname, offset, pattern, start + 2, length)) return false;
  } else if (!(count === 1 && pattern[start] === '*') &&
             (hostname.length !== count || !equalAt(hostname, 0, pattern, start, count))) return false;
  return glob(pattern, path, slash);
 };
 const any = (patterns, test) => {
  for (let i = 0; i < patterns.length; i++) if (test(patterns[i])) return true;
  return false;
 };
 const urlGlob = pattern => glob(pattern, address, 0);
 if (!(any(args[3], match) || any(args[5], urlGlob)) || any(args[4], match) || any(args[6], urlGlob)) return;
 const id = args[0], revision = args[1], source = args[2];

"""
}
