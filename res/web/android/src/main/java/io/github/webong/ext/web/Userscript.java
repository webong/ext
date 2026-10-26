package io.github.webong.ext.web;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/** The userscript rules of {@code res/web/userscript} (ext.userscript/v1alpha1). */
public final class Userscript {
    private Userscript() {}

    private static final int MAX_SOURCE_BYTES = 1024 * 1024;
    private static final int MAX_METADATA_BYTES = 64 * 1024;
    private static final Set<String> DESCRIPTIVE_DIRECTIVES = new HashSet<>(java.util.Arrays.asList(
            "antifeature", "author", "copyright", "defaulticon", "description", "homepage",
            "homepageurl", "icon", "icon64", "icon64url", "iconurl", "license", "namespace",
            "source", "supporturl", "tag", "version", "website"));

    /** Reads Greasemonkey-family metadata without weakening unsupported requirements. */
    public static UserscriptCompatibilityReport compatibility(String source) {
        String name = "", runAt = "", world = "";
        List<String> grants = new ArrayList<>(), matches = new ArrayList<>(), excludeMatches = new ArrayList<>();
        List<String> includes = new ArrayList<>(), excludes = new ArrayList<>(), blockers = new ArrayList<>();
        Set<String> seenBlockers = new HashSet<>();
        boolean allFrames = true, inBlock = false, found = false, ended = false;
        int headerBytes = 0;
        if (source.isEmpty() || GoText.utf8Length(source) > MAX_SOURCE_BYTES) block(blockers, seenBlockers, "invalid_source_size");
        for (String raw : GoText.split(source, '\n')) {
            String line = GoText.trimSpace(raw);
            if (line.equals("// ==UserScript==")) { inBlock = true; found = true; continue; }
            if (line.equals("// ==/UserScript==") && inBlock) { ended = true; break; }
            if (!inBlock) continue;
            headerBytes += GoText.utf8Length(raw) + 1;
            if (headerBytes > MAX_METADATA_BYTES) { block(blockers, seenBlockers, "metadata_too_large"); break; }
            String[] directive = metadataDirective(line);
            if (directive == null) continue;
            String directiveName = directive[0].toLowerCase(java.util.Locale.ROOT);
            switch (directiveName) {
                case "name": name = directive[1]; break;
                case "grant": grants.add(directive[1]); if (!directive[1].equals("none")) block(blockers, seenBlockers, "unsupported_grant"); break;
                case "match": matches.add(directive[1]); break;
                case "exclude-match": excludeMatches.add(directive[1]); break;
                case "include": includes.add(directive[1]); block(blockers, seenBlockers, "unsupported_include"); break;
                case "exclude": excludes.add(directive[1]); block(blockers, seenBlockers, "unsupported_exclude"); break;
                case "noframes": allFrames = false; block(blockers, seenBlockers, "unsupported_noframes"); break;
                case "run-at": runAt = directive[1]; if (!runAt.equals("document-start")) block(blockers, seenBlockers, "unsupported_run_at"); break;
                case "inject-into": world = directive[1]; if (!world.equals("page")) block(blockers, seenBlockers, "unsupported_world"); break;
                case "require": block(blockers, seenBlockers, "unsupported_require"); break;
                case "resource": block(blockers, seenBlockers, "unsupported_resource"); break;
                case "connect": case "run-in": case "sandbox": case "unwrap": case "top-level-await": case "webrequest":
                    block(blockers, seenBlockers, "unsupported_directive"); break;
                default: if (!DESCRIPTIVE_DIRECTIVES.contains(directiveName)) {
                    block(blockers, seenBlockers, "unsupported_directive");
                }
            }
        }
        if (!found || !ended) block(blockers, seenBlockers, "missing_header");
        return new UserscriptCompatibilityReport(name, grants, matches, excludeMatches, includes, excludes,
                runAt, world, allFrames, blockers, Collections.<String>emptyList());
    }

    /**
     * {@code userscript.Revision}: {@code sha256:} and the digest of the compact JSON object
     * {@code {"target","id","name","matches","excludeMatches","source"}}, written as Go writes it.
     */
    public static String revision(UserscriptRecord record) {
        StringBuilder payload = new StringBuilder("{\"target\":");
        GoText.jsonQuote(payload, record.target).append(",\"id\":");
        GoText.jsonQuote(payload, record.id).append(",\"name\":");
        GoText.jsonQuote(payload, record.name).append(",\"matches\":");
        GoText.jsonArray(payload, record.matches).append(",\"excludeMatches\":");
        GoText.jsonArray(payload, record.excludeMatches).append(",\"source\":");
        GoText.jsonQuote(payload, record.source).append('}');
        byte[] digest;
        try {
            digest = MessageDigest.getInstance("SHA-256").digest(payload.toString().getBytes(StandardCharsets.UTF_8));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
        StringBuilder out = new StringBuilder("sha256:");
        for (byte b : digest) {
            out.append(Character.forDigit((b >> 4) & 15, 16)).append(Character.forDigit(b & 15, 16));
        }
        return out.toString();
    }

    /**
     * {@code userscript.Validate}: document-start, {@code @grant none}, page world and explicit
     * match patterns ({@code <all_urls>} is not accepted).
     */
    public static void validate(UserscriptRecord record) throws UserscriptException {
        if (GoText.isBlank(record.target) || !record.target.equals(GoText.trimSpace(record.target))
                || GoText.utf8Length(record.target) > 512) {
            throw reject("invalid_target", "userscript target must be a non-empty stable identifier");
        }
        if (!isValidId(record.id)) {
            throw reject("invalid_id", "userscript id must contain 1–128 letters, digits, dots, underscores, or hyphens");
        }
        if (GoText.isBlank(record.name) || GoText.utf8Length(record.name) > 128) {
            throw reject("invalid_name", "userscript name must contain 1–128 characters");
        }
        if (record.source.isEmpty() || GoText.utf8Length(record.source) > MAX_SOURCE_BYTES) {
            throw reject("invalid_source_size", "userscript source must contain 1 byte to 1 MiB");
        }
        if (!record.revision.equals(revision(record))) {
            throw reject("revision_mismatch", "userscript revision does not match source, target, or permissions");
        }
        List<String> matches = record.matches == null ? Collections.<String>emptyList() : record.matches;
        List<String> excludes = record.excludeMatches == null ? Collections.<String>emptyList() : record.excludeMatches;
        if (matches.isEmpty() || matches.size() > 128 || excludes.size() > 128) {
            throw reject("invalid_matches", "userscript requires 1–128 matches and at most 128 exclusions");
        }
        Set<String> seen = new HashSet<>();
        List<String> patterns = new ArrayList<>(matches);
        patterns.addAll(excludes);
        for (String pattern : patterns) {
            if (GoText.utf8Length(pattern) > 512 || !isMatchPattern(pattern) || seen.contains(pattern)) {
                throw reject("invalid_match_pattern", "invalid or repeated userscript match pattern");
            }
            if (pattern.startsWith("file://") && !pattern.startsWith("file://*/")) {
                throw reject("invalid_file_host", "file userscript match host must be *");
            }
            seen.add(pattern);
        }
        for (String line : GoText.split(record.source, '\n')) {
            String trimmed = GoText.trimSpace(line);
            if (trimmed.startsWith("// @grant ") && !trimmed.equals("// @grant none")) {
                throw reject("unsupported_grant", "extension-free userscripts support @grant none only");
            }
            if (trimmed.startsWith("// @require ") || trimmed.startsWith("// @resource ")) {
                throw reject("unsupported_require", "extension-free userscripts do not support @require or @resource");
            }
            if (trimmed.startsWith("// @include ") || trimmed.startsWith("// @exclude ") || trimmed.startsWith("// @connect ")
                    || trimmed.startsWith("// @updateURL ") || trimmed.startsWith("// @downloadURL ")
                    || trimmed.equals("// @noframes")) {
                throw reject("unsupported_directive", "extension-free userscript metadata contains an unsupported directive");
            }
            if (trimmed.startsWith("// @run-at ") && !trimmed.equals("// @run-at document-start")) {
                throw reject("unsupported_run_at", "extension-free userscripts run at document-start only");
            }
            if (trimmed.startsWith("// @inject-into ") && !trimmed.equals("// @inject-into page")) {
                throw reject("unsupported_world", "extension-free userscripts run in the page world only");
            }
        }
        validateSourceMetadata(record);
    }

    /**
     * {@code userscript.InferMetadata}: fills an omitted name, id and scope from the script header.
     * Fields already set are kept; {@link #validate} checks them against the header.
     * {@code sourceFile} names the file the script came from (a {@code /}-separated path, or "").
     */
    public static UserscriptRecord inferMetadata(UserscriptRecord record, String sourceFile) {
        String name = "";
        List<String> matches = null;
        List<String> excludes = null;
        boolean inBlock = false;
        for (String raw : GoText.split(record.source, '\n')) {
            String line = GoText.trimSpace(raw);
            if (line.equals("// ==UserScript==")) {
                inBlock = true;
                continue;
            }
            if (line.equals("// ==/UserScript==")) {
                break;
            }
            if (!inBlock) {
                continue;
            }
            String value = GoText.cutPrefix(line, "// @name ");
            if (value != null) {
                name = GoText.trimSpace(value);
            }
            value = GoText.cutPrefix(line, "// @match ");
            if (value != null) {
                matches = append(matches, GoText.trimSpace(value));
            }
            value = GoText.cutPrefix(line, "// @exclude-match ");
            if (value != null) {
                excludes = append(excludes, GoText.trimSpace(value));
            }
        }
        String newName = record.name;
        if (newName.isEmpty()) {
            newName = name;
            if (newName.isEmpty() && !sourceFile.isEmpty()) {
                String base = fileBase(sourceFile);
                String ext = fileExtension(base);
                newName = base.substring(0, base.length() - ext.length());
            }
        }
        String newId = record.id.isEmpty() ? derivedId(newName) : record.id;
        return new UserscriptRecord(record.target, newId, newName, record.revision,
                record.matches == null ? matches : record.matches,
                record.excludeMatches == null ? excludes : record.excludeMatches, record.enabled, record.source);
    }

    /**
     * {@code userscript.ReplaySource}: the page-world, URL-scoped script to register for a reviewed
     * userscript. A document-local marker keeps it from running twice when attaching races
     * navigation. Register it to run at document start, for example with
     * {@code WebViewCompat.addDocumentStartJavaScript}.
     */
    public static String replaySource(String id, String revision, String source, List<String> matches,
            List<String> excludes) throws UserscriptException {
        UserscriptRecord record = new UserscriptRecord("session", id, id, matches, excludes, source);
        record = record.withRevision(revision(record));
        validate(record);
        if (revision.isEmpty()) {
            throw new UserscriptException("", "userscript registration needs a reviewed revision");
        }
        StringBuilder out = new StringBuilder(REPLAY_PREFIX).append('[');
        GoText.jsonQuote(out, id).append(',');
        GoText.jsonQuote(out, revision).append(',');
        GoText.jsonQuote(out, source).append(',');
        GoText.jsonArray(out, matches).append(',');
        GoText.jsonArray(out, excludes).append(']');
        return out.append(REPLAY_SUFFIX).toString();
    }

    // -------------------------------------------------------------------------------------------

    private static UserscriptException reject(String code, String message) {
        return new UserscriptException(code, message);
    }

    private static void block(List<String> blockers, Set<String> seen, String code) {
        if (seen.add(code)) blockers.add(code);
    }

    private static String[] metadataDirective(String line) {
        String body = GoText.cutPrefix(line, "// @");
        if (body == null) return null;
        body = GoText.trimSpace(body);
        if (body.isEmpty()) return null;
        int space = -1;
        for (int i = 0; i < body.length(); i++) {
            if (Character.isWhitespace(body.charAt(i))) { space = i; break; }
        }
        return space < 0 ? new String[] {body, ""}
                : new String[] {body.substring(0, space), GoText.trimSpace(body.substring(space + 1))};
    }

    private static List<String> append(List<String> list, String item) {
        List<String> out = list == null ? new ArrayList<String>() : new ArrayList<>(list);
        out.add(item);
        return out;
    }

    private static boolean isAlnum(char c) {
        return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z');
    }

    /** {@code ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$}, with {@code $} meaning the end of the text. */
    private static boolean isValidId(String id) {
        if (id.isEmpty() || id.length() > 128 || !isAlnum(id.charAt(0))) {
            return false;
        }
        for (int i = 1; i < id.length(); i++) {
            char c = id.charAt(i);
            if (!isAlnum(c) && c != '.' && c != '_' && c != '-') {
                return false;
            }
        }
        return true;
    }

    /**
     * {@code ^(\*|https?|file)://([^/]+|\*)/(.*)$}: a scheme, a non-empty host, a slash, and a path
     * with no newline.
     */
    private static boolean isMatchPattern(String pattern) {
        for (String scheme : new String[] {"*://", "http://", "https://", "file://"}) {
            if (!pattern.startsWith(scheme)) {
                continue;
            }
            int slash = pattern.indexOf('/', scheme.length());
            if (slash <= scheme.length()) {
                continue;
            }
            return pattern.indexOf('\n', slash + 1) < 0;
        }
        return false;
    }

    private static void validateSourceMetadata(UserscriptRecord record) throws UserscriptException {
        boolean inBlock = false;
        boolean found = false;
        List<String> matches = new ArrayList<>();
        List<String> excludes = new ArrayList<>();
        for (String raw : GoText.split(record.source, '\n')) {
            String line = GoText.trimSpace(raw);
            if (line.equals("// ==UserScript==")) {
                inBlock = true;
                found = true;
                continue;
            }
            if (line.equals("// ==/UserScript==")) {
                break;
            }
            if (!inBlock || !line.startsWith("// @")) {
                continue;
            }
            String value = GoText.cutPrefix(line, "// @match ");
            if (value != null) {
                matches.add(GoText.trimSpace(value));
            }
            value = GoText.cutPrefix(line, "// @exclude-match ");
            if (value != null) {
                excludes.add(GoText.trimSpace(value));
            }
        }
        if (!found) {
            return;
        }
        if (!sameStrings(matches, record.matches) || !sameStrings(excludes, record.excludeMatches)) {
            throw reject("match_metadata_mismatch", "userscript source match metadata must match --match and --exclude-match");
        }
    }

    private static boolean sameStrings(List<String> left, List<String> right) {
        List<String> a = new ArrayList<>(left);
        List<String> b = right == null ? new ArrayList<String>() : new ArrayList<>(right);
        Collections.sort(a);
        Collections.sort(b);
        return a.equals(b);
    }

    /** {@code strings.Trim(regexp("[^A-Za-z0-9._-]+").ReplaceAllString(strings.ToLower(name), "-"), "-.")}. */
    private static String derivedId(String name) {
        StringBuilder out = new StringBuilder();
        boolean inRun = false;
        for (int i = 0; i < name.length(); ) {
            int c = GoText.lowercase(name.codePointAt(i));
            i += Character.charCount(name.codePointAt(i));
            if (c < 0x80 && (isAlnum((char) c) || c == '.' || c == '_' || c == '-')) {
                out.append((char) c);
                inRun = false;
            } else if (!inRun) {
                out.append('-');
                inRun = true;
            }
        }
        int start = 0;
        int end = out.length();
        while (start < end && (out.charAt(start) == '-' || out.charAt(start) == '.')) {
            start++;
        }
        while (end > start && (out.charAt(end - 1) == '-' || out.charAt(end - 1) == '.')) {
            end--;
        }
        return out.substring(start, end);
    }

    /** {@code filepath.Base} for a {@code /}-separated path. */
    private static String fileBase(String path) {
        if (path.isEmpty()) {
            return ".";
        }
        int end = path.length();
        while (end > 0 && path.charAt(end - 1) == '/') {
            end--;
        }
        if (end == 0) {
            return "/";
        }
        String trimmed = path.substring(0, end);
        int slash = trimmed.lastIndexOf('/');
        return slash >= 0 ? trimmed.substring(slash + 1) : trimmed;
    }

    /** {@code filepath.Ext}: from the last dot of the final element. */
    private static String fileExtension(String base) {
        int dot = base.lastIndexOf('.');
        return dot >= 0 ? base.substring(dot) : "";
    }

    // The registration script around the JSON values; the text of res/web/userscript/replay.go,
    // which the replay fixture checks.
    private static final String REPLAY_PREFIX =
            "(() => {\n"
            + " const [id, revision, source, matches, excludes] = ";

    private static final String REPLAY_SUFFIX =
            ";\n"
            + " const glob = value => new RegExp('^' + value.replace(/[.+?^${}()|[\\]\\\\]/g, '\\\\$&').replaceAll('*', '.*') + '$');\n"
            + " const allowed = pattern => {\n"
            + "  const parts = pattern.match(/^(\\*|https?|file):\\/\\/([^/]+)\\/(.*)$/);\n"
            + "  if (!parts) return false;\n"
            + "  const url = new URL(location.href);\n"
            + "  if (parts[1] === '*' ? !['http:', 'https:'].includes(url.protocol) : url.protocol !== parts[1]+':') return false;\n"
            + "  const host = parts[2];\n"
            + "  if (host !== '*' && !(host.startsWith('*.') ? url.hostname === host.slice(2) || url.hostname.endsWith(host.slice(1)) : url.hostname === host)) return false;\n"
            + "  return glob('/'+parts[3]).test(url.pathname+url.search);\n"
            + " };\n"
            + " if (!matches.some(allowed) || (excludes || []).some(allowed)) return;\n"
            + " const applied = globalThis[Symbol.for('ctx.userscripts.applied')] ||= new Map();\n"
            + " if (applied.get(id) === revision) return;\n"
            + " applied.set(id, revision);\n"
            + " (0, eval)(source);\n"
            + "})()";
}
