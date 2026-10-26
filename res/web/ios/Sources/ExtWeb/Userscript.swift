import CryptoKit
import Foundation

/// A userscript the caller has reviewed, as `res/web/userscript` stores it
/// (ext.userscript/v1alpha1).
///
/// `matches` and `excludeMatches` are optional because the revision digest tells nil
/// (`null`) from an empty list (`[]`).
public struct UserscriptRecord: Equatable, Sendable, Codable {
    public var target: String
    public var id: String
    public var name: String
    public var revision: String
    public var matches: [String]?
    public var excludeMatches: [String]?
    public var enabled: Bool
    public var source: String

    public init(
        target: String, id: String, name: String, revision: String = "", matches: [String]? = nil,
        excludeMatches: [String]? = nil, enabled: Bool = false, source: String
    ) {
        self.target = target
        self.id = id
        self.name = name
        self.revision = revision
        self.matches = matches
        self.excludeMatches = excludeMatches
        self.enabled = enabled
        self.source = source
    }

    private enum CodingKeys: String, CodingKey {
        case target, id, name, revision, matches, excludeMatches, enabled, source
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        target = try container.decodeIfPresent(String.self, forKey: .target) ?? ""
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        revision = try container.decodeIfPresent(String.self, forKey: .revision) ?? ""
        matches = try container.decodeIfPresent([String].self, forKey: .matches)
        excludeMatches = try container.decodeIfPresent([String].self, forKey: .excludeMatches)
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        source = try container.decodeIfPresent(String.self, forKey: .source) ?? ""
    }

    /// Every member is written, and an absent list is `null`, as Go's encoder does.
    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(target, forKey: .target)
        try container.encode(id, forKey: .id)
        try container.encode(name, forKey: .name)
        try container.encode(revision, forKey: .revision)
        try container.encode(matches, forKey: .matches)
        try container.encode(excludeMatches, forKey: .excludeMatches)
        try container.encode(enabled, forKey: .enabled)
        try container.encode(source, forKey: .source)
    }
}

/// A record that breaks ext.userscript/v1alpha1. `code` is stable and language-neutral (it is
/// what the fixtures check); `message` is for people. Failures that are not a record rule,
/// such as an empty revision to `replaySource`, have the code `""`.
public struct UserscriptError: Error, Equatable, CustomStringConvertible {
    public let code: String
    public let message: String
    public var description: String { message }
}

/// Import-time metadata assessment. Unsupported requirements remain visible as
/// stable blocker codes; inspection does not install or execute the source.
public struct UserscriptCompatibilityReport: Equatable, Sendable, Codable {
    public var name: String
    public var grants: [String]
    public var matches: [String]
    public var excludeMatches: [String]
    public var includes: [String]
    public var excludes: [String]
    public var runAt: String
    public var world: String
    public var allFrames: Bool
    public var blockers: [String]
    public var notes: [String]
    public var runnable: Bool { blockers.isEmpty }
}

public enum Userscript {
    private static let maxSourceBytes = 1024 * 1024
    private static let maxMetadataBytes = 64 * 1024
    private static let descriptiveDirectives: Set<String> = [
        "antifeature", "author", "copyright", "defaulticon", "description", "homepage",
        "homepageurl", "icon", "icon64", "icon64url", "iconurl", "license", "namespace",
        "source", "supporturl", "tag", "version", "website",
    ]

    /// Reads Greasemonkey-family metadata without weakening unsupported requirements.
    public static func compatibility(of source: String) -> UserscriptCompatibilityReport {
        var report = UserscriptCompatibilityReport(
            name: "", grants: [], matches: [], excludeMatches: [], includes: [], excludes: [],
            runAt: "", world: "", allFrames: true, blockers: [], notes: [])
        var seen = Set<String>()
        func block(_ code: String) {
            if seen.insert(code).inserted { report.blockers.append(code) }
        }
        if source.isEmpty || source.utf8.count > maxSourceBytes { block("invalid_source_size") }
        var inBlock = false, found = false, ended = false, headerBytes = 0
        for rawLine in GoText.split(source, on: 0x0A) {
            let line = GoText.trimSpace(rawLine)
            if line == "// ==UserScript==" { inBlock = true; found = true; continue }
            if line == "// ==/UserScript==" && inBlock { ended = true; break }
            if !inBlock { continue }
            headerBytes += rawLine.utf8.count + 1
            if headerBytes > maxMetadataBytes { block("metadata_too_large"); break }
            guard let directive = metadataDirective(line) else { continue }
            let name = directive.name.lowercased()
            switch name {
            case "name": report.name = directive.value
            case "grant":
                report.grants.append(directive.value)
                if directive.value != "none" { block("unsupported_grant") }
            case "match": report.matches.append(directive.value)
            case "exclude-match": report.excludeMatches.append(directive.value)
            case "include": report.includes.append(directive.value); block("unsupported_include")
            case "exclude": report.excludes.append(directive.value); block("unsupported_exclude")
            case "noframes": report.allFrames = false; block("unsupported_noframes")
            case "run-at":
                report.runAt = directive.value
                if directive.value != "document-start" { block("unsupported_run_at") }
            case "inject-into":
                report.world = directive.value
                if directive.value != "page" { block("unsupported_world") }
            case "require": block("unsupported_require")
            case "resource": block("unsupported_resource")
            case "connect", "run-in", "sandbox", "unwrap", "top-level-await", "webrequest":
                block("unsupported_directive")
            default:
                if !descriptiveDirectives.contains(name) { block("unsupported_directive") }
            }
        }
        if !found || !ended { block("missing_header") }
        return report
    }

    /// `userscript.Revision`: `sha256:` and the digest of the compact JSON object
    /// `{"target","id","name","matches","excludeMatches","source"}`, written as Go writes it.
    public static func revision(of record: UserscriptRecord) -> String {
        let payload =
            "{\"target\":" + GoJSON.quote(record.target) + ",\"id\":" + GoJSON.quote(record.id)
            + ",\"name\":" + GoJSON.quote(record.name) + ",\"matches\":" + GoJSON.array(record.matches)
            + ",\"excludeMatches\":" + GoJSON.array(record.excludeMatches) + ",\"source\":"
            + GoJSON.quote(record.source) + "}"
        let digest = SHA256.hash(data: Data(payload.utf8))
        return "sha256:" + digest.map { String(format: "%02x", $0) }.joined()
    }

    /// `userscript.Validate`: document-start, `@grant none`, page world and explicit match
    /// patterns (`<all_urls>` is not accepted).
    public static func validate(_ record: UserscriptRecord) throws {
        if GoText.isBlank(record.target) || record.target != GoText.trimSpace(record.target) || record.target.utf8.count > 512 {
            throw reject("invalid_target", "userscript target must be a non-empty stable identifier")
        }
        if !isValidID(record.id) {
            throw reject("invalid_id", "userscript id must contain 1–128 letters, digits, dots, underscores, or hyphens")
        }
        if GoText.isBlank(record.name) || record.name.utf8.count > 128 {
            throw reject("invalid_name", "userscript name must contain 1–128 characters")
        }
        if record.source.isEmpty || record.source.utf8.count > maxSourceBytes {
            throw reject("invalid_source_size", "userscript source must contain 1 byte to 1 MiB")
        }
        if record.revision != revision(of: record) {
            throw reject("revision_mismatch", "userscript revision does not match source, target, or permissions")
        }
        let matches = record.matches ?? []
        let excludes = record.excludeMatches ?? []
        if matches.isEmpty || matches.count > 128 || excludes.count > 128 {
            throw reject("invalid_matches", "userscript requires 1–128 matches and at most 128 exclusions")
        }
        var seen = Set<String>()
        for pattern in matches + excludes {
            if pattern.utf8.count > 512 || !isMatchPattern(pattern) || seen.contains(pattern) {
                throw reject("invalid_match_pattern", "invalid or repeated userscript match pattern")
            }
            if GoText.hasPrefix(pattern, "file://") && !GoText.hasPrefix(pattern, "file://*/") {
                throw reject("invalid_file_host", "file userscript match host must be *")
            }
            seen.insert(pattern)
        }
        for line in GoText.split(record.source, on: 0x0A) {
            let trimmed = GoText.trimSpace(line)
            if GoText.hasPrefix(trimmed, "// @grant ") && trimmed != "// @grant none" {
                throw reject("unsupported_grant", "extension-free userscripts support @grant none only")
            }
            if GoText.hasPrefix(trimmed, "// @require ") || GoText.hasPrefix(trimmed, "// @resource ") {
                throw reject("unsupported_require", "extension-free userscripts do not support @require or @resource")
            }
            if GoText.hasPrefix(trimmed, "// @include ") || GoText.hasPrefix(trimmed, "// @exclude ")
                || GoText.hasPrefix(trimmed, "// @connect ") || GoText.hasPrefix(trimmed, "// @updateURL ")
                || GoText.hasPrefix(trimmed, "// @downloadURL ") || trimmed == "// @noframes"
            {
                throw reject("unsupported_directive", "extension-free userscript metadata contains an unsupported directive")
            }
            if GoText.hasPrefix(trimmed, "// @run-at ") && trimmed != "// @run-at document-start" {
                throw reject("unsupported_run_at", "extension-free userscripts run at document-start only")
            }
            if GoText.hasPrefix(trimmed, "// @inject-into ") && trimmed != "// @inject-into page" {
                throw reject("unsupported_world", "extension-free userscripts run in the page world only")
            }
        }
        try validateSourceMetadata(record)
    }

    /// `userscript.InferMetadata`: fills an omitted name, id and scope from the script header.
    /// Fields already set are kept; `validate` checks them against the header.
    /// `sourceFile` names the file the script came from (a `/`-separated path).
    public static func inferMetadata(_ record: UserscriptRecord, sourceFile: String = "") -> UserscriptRecord {
        var record = record
        var name = ""
        var matches: [String]?
        var excludes: [String]?
        var inBlock = false
        for rawLine in GoText.split(record.source, on: 0x0A) {
            let line = GoText.trimSpace(rawLine)
            if line == "// ==UserScript==" {
                inBlock = true
                continue
            }
            if line == "// ==/UserScript==" { break }
            if !inBlock { continue }
            if let value = GoText.cutPrefix(line, "// @name ") { name = GoText.trimSpace(value) }
            if let value = GoText.cutPrefix(line, "// @match ") { matches = (matches ?? []) + [GoText.trimSpace(value)] }
            if let value = GoText.cutPrefix(line, "// @exclude-match ") {
                excludes = (excludes ?? []) + [GoText.trimSpace(value)]
            }
        }
        if record.name.isEmpty {
            record.name = name
            if record.name.isEmpty && !sourceFile.isEmpty {
                let base = fileBase(sourceFile)
                let ext = fileExtension(base)
                record.name = ext.isEmpty ? base : String(decoding: Array(base.utf8.dropLast(ext.utf8.count)), as: UTF8.self)
            }
        }
        if record.id.isEmpty {
            record.id = derivedID(from: record.name)
        }
        if record.matches == nil { record.matches = matches }
        if record.excludeMatches == nil { record.excludeMatches = excludes }
        return record
    }

    /// `userscript.ReplaySource`: the page-world, URL-scoped script to register for a reviewed
    /// userscript. A document-local marker keeps it from running twice when attaching races
    /// navigation. Register it to run at document start, for example as a `WKUserScript`.
    public static func replaySource(
        id: String, revision: String, source: String, matches: [String]?, excludes: [String]?
    ) throws -> String {
        var record = UserscriptRecord(
            target: "session", id: id, name: id, matches: matches, excludeMatches: excludes, source: source)
        record.revision = Userscript.revision(of: record)
        try validate(record)
        if revision.isEmpty {
            throw UserscriptError(code: "", message: "userscript registration needs a reviewed revision")
        }
        let values =
            "[" + [GoJSON.quote(id), GoJSON.quote(revision), GoJSON.quote(source), GoJSON.array(matches), GoJSON.array(excludes)]
            .joined(separator: ",") + "]"
        return replayPrefix + values + replaySuffix
    }

    // MARK: -

    private static func reject(_ code: String, _ message: String) -> UserscriptError {
        UserscriptError(code: code, message: message)
    }

    private static func metadataDirective(_ line: String) -> (name: String, value: String)? {
        guard let body = GoText.cutPrefix(line, "// @") else { return nil }
        let value = GoText.trimSpace(body)
        guard !value.isEmpty else { return nil }
        guard let space = value.firstIndex(where: { $0 == " " || $0 == "\t" }) else { return (value, "") }
        return (String(value[..<space]), GoText.trimSpace(String(value[value.index(after: space)...])))
    }

    /// `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`, with `$` meaning the end of the text.
    private static func isValidID(_ id: String) -> Bool {
        let bytes = Array(id.utf8)
        guard (1...128).contains(bytes.count), isAlnum(bytes[0]) else { return false }
        return bytes.dropFirst().allSatisfy { isAlnum($0) || $0 == UInt8(ascii: ".") || $0 == UInt8(ascii: "_") || $0 == UInt8(ascii: "-") }
    }

    private static func isAlnum(_ byte: UInt8) -> Bool {
        (byte >= 0x30 && byte <= 0x39) || (byte >= 0x41 && byte <= 0x5A) || (byte >= 0x61 && byte <= 0x7A)
    }

    /// `^(\*|https?|file)://([^/]+|\*)/(.*)$`: a scheme, a non-empty host, a slash, and a path
    /// with no newline.
    private static func isMatchPattern(_ pattern: String) -> Bool {
        let bytes = Array(pattern.utf8)
        for scheme in ["*://", "http://", "https://", "file://"] {
            let prefix = Array(scheme.utf8)
            guard bytes.starts(with: prefix) else { continue }
            let rest = bytes.dropFirst(prefix.count)
            guard let slash = rest.firstIndex(of: UInt8(ascii: "/")), slash > rest.startIndex else { continue }
            return !rest[(slash + 1)...].contains(0x0A)
        }
        return false
    }

    private static func validateSourceMetadata(_ record: UserscriptRecord) throws {
        var inBlock = false
        var found = false
        var matches: [String] = []
        var excludes: [String] = []
        for rawLine in GoText.split(record.source, on: 0x0A) {
            let line = GoText.trimSpace(rawLine)
            if line == "// ==UserScript==" {
                inBlock = true
                found = true
                continue
            }
            if line == "// ==/UserScript==" {
                inBlock = false
                break
            }
            if !inBlock || !GoText.hasPrefix(line, "// @") { continue }
            if let value = GoText.cutPrefix(line, "// @match ") { matches.append(GoText.trimSpace(value)) }
            if let value = GoText.cutPrefix(line, "// @exclude-match ") { excludes.append(GoText.trimSpace(value)) }
        }
        if !found { return }
        if matches.sorted() != (record.matches ?? []).sorted() || excludes.sorted() != (record.excludeMatches ?? []).sorted() {
            throw reject("match_metadata_mismatch", "userscript source match metadata must match --match and --exclude-match")
        }
    }

    /// `strings.Trim(regexp("[^A-Za-z0-9._-]+").ReplaceAllString(strings.ToLower(name), "-"), "-.")`.
    private static func derivedID(from name: String) -> String {
        var out: [UInt8] = []
        var inRun = false
        for scalar in name.unicodeScalars {
            let lowered = GoText.lowercase(scalar)
            if lowered.value < 0x80, let byte = UInt8(exactly: lowered.value),
                isAlnum(byte) || byte == UInt8(ascii: ".") || byte == UInt8(ascii: "_") || byte == UInt8(ascii: "-")
            {
                out.append(byte)
                inRun = false
            } else if !inRun {
                out.append(UInt8(ascii: "-"))
                inRun = true
            }
        }
        while let first = out.first, first == UInt8(ascii: "-") || first == UInt8(ascii: ".") { out.removeFirst() }
        while let last = out.last, last == UInt8(ascii: "-") || last == UInt8(ascii: ".") { out.removeLast() }
        return String(decoding: out, as: UTF8.self)
    }

    /// `filepath.Base` for a `/`-separated path.
    private static func fileBase(_ path: String) -> String {
        if path.isEmpty { return "." }
        var bytes = Array(path.utf8)
        while bytes.count > 0, bytes.last == UInt8(ascii: "/") { bytes.removeLast() }
        if bytes.isEmpty { return "/" }
        if let slash = bytes.lastIndex(of: UInt8(ascii: "/")) { bytes = Array(bytes[(slash + 1)...]) }
        return String(decoding: bytes, as: UTF8.self)
    }

    /// `filepath.Ext`: from the last dot of the final element.
    private static func fileExtension(_ base: String) -> String {
        let bytes = Array(base.utf8)
        guard let dot = bytes.lastIndex(of: UInt8(ascii: ".")) else { return "" }
        return String(decoding: bytes[dot...], as: UTF8.self)
    }

    // The registration script around the JSON values; the text of res/web/userscript/replay.go, which
    // the replay fixture checks.
    private static let replayPrefix = "(() => {\n const [id, revision, source, matches, excludes] = "
    private static let replaySuffix = #"""
;
 const glob = value => new RegExp('^' + value.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replaceAll('*', '.*') + '$');
 const allowed = pattern => {
  const parts = pattern.match(/^(\*|https?|file):\/\/([^/]+)\/(.*)$/);
  if (!parts) return false;
  const url = new URL(location.href);
  if (parts[1] === '*' ? !['http:', 'https:'].includes(url.protocol) : url.protocol !== parts[1]+':') return false;
  const host = parts[2];
  if (host !== '*' && !(host.startsWith('*.') ? url.hostname === host.slice(2) || url.hostname.endsWith(host.slice(1)) : url.hostname === host)) return false;
  return glob('/'+parts[3]).test(url.pathname+url.search);
 };
 if (!matches.some(allowed) || (excludes || []).some(allowed)) return;
 const applied = globalThis[Symbol.for('ctx.userscripts.applied')] ||= new Map();
 if (applied.get(id) === revision) return;
 applied.set(id, revision);
 (0, eval)(source);
})()
"""#
}
