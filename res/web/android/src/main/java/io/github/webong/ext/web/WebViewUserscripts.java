package io.github.webong.ext.web;

import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.regex.Pattern;

/** Opt-in page-world userscript profiles for embedded WebViews. */
public final class WebViewUserscripts {
    private static final String DOMAIN = "ext.webview-userscript/v1\n";
    private static final Pattern URL_GLOB = Pattern.compile("https?://[a-z0-9-]+(?:\\.[a-z0-9-]+)*/[!-~]*");

    private WebViewUserscripts() {}

    public enum Timing {
        DOCUMENT_START("document-start"), DOCUMENT_END("document-end"), DOCUMENT_IDLE("document-idle");

        public final String directive;
        Timing(String directive) { this.directive = directive; }
    }

    public enum Frames { MAIN, ALL }

    /** An exact combination the native host can register in the page world. */
    public static final class Profile {
        public final Timing timing;
        public final Frames frames;

        public Profile(Timing timing, Frames frames) {
            this.timing = java.util.Objects.requireNonNull(timing);
            this.frames = java.util.Objects.requireNonNull(frames);
        }

        @Override public boolean equals(Object value) {
            if (!(value instanceof Profile)) return false;
            Profile other = (Profile) value;
            return timing == other.timing && frames == other.frames;
        }

        @Override public int hashCode() { return 31 * timing.hashCode() + frames.hashCode(); }
    }

    public static final class Registration {
        public final Profile profile;
        public final String source;

        private Registration(Profile profile, String source) {
            this.profile = profile;
            this.source = source;
        }
    }

    /** Content identity in the v1 profile. The caller supplies approval. */
    public static String revision(UserscriptRecord record) {
        return Userscript.revision(record, DOMAIN);
    }

    /** Checks the entire batch before a host changes any native registration. */
    public static List<Registration> prepare(List<UserscriptRecord> records, String target,
            List<Profile> supported) throws UserscriptException {
        long bytes = 0;
        for (UserscriptRecord record : records) bytes += GoText.utf8Length(record.source);
        if (records.size() > 512 || bytes > (8 << 20)) throw new IllegalArgumentException("userscript batch exceeds limit");
        Set<String> ids = new HashSet<>();
        List<Registration> registrations = new ArrayList<>();
        for (UserscriptRecord record : records) {
            Userscript.validateIdentity(record, DOMAIN);
            if (!record.target.equals(target) || !ids.add(record.id)) {
                throw new IllegalArgumentException("userscript target mismatch or duplicate id");
            }
            UserscriptCompatibilityReport metadata = Userscript.compatibility(record.source, true);
            if (!metadata.blockers.isEmpty()) {
                throw new UserscriptException(metadata.blockers.get(0), "unsupported WebView userscript metadata");
            }
            Timing timing = timingFor(metadata.runAt);
            if (!Userscript.sameStrings(metadata.matches, record.matches)
                    || !Userscript.sameStrings(metadata.excludeMatches, record.excludeMatches)) {
                throw new UserscriptException("match_metadata_mismatch", "WebView userscript record scope disagrees with its header");
            }
            Userscript.validateMatchPatterns(record, !metadata.includes.isEmpty());
            validateGlobs(metadata.includes, metadata.excludes);
            if (!record.enabled) continue;
            Profile profile = new Profile(timing, metadata.allFrames ? Frames.ALL : Frames.MAIN);
            if (!supported.contains(profile)) {
                throw new UserscriptException("unsupported_profile", "the selected WebView cannot register this timing and frame scope");
            }
            registrations.add(new Registration(profile, replay(record, metadata)));
        }
        return registrations;
    }

    private static Timing timingFor(String name) throws UserscriptException {
        for (Timing timing : Timing.values()) if (timing.directive.equals(name)) return timing;
        throw new UserscriptException("unsupported_run_at", "WebView userscripts require an explicit document timing");
    }

    private static void validateGlobs(List<String> includes, List<String> excludes) throws UserscriptException {
        if (includes.size() > 128 || excludes.size() > 128) {
            throw new UserscriptException("invalid_url_glob", "too many WebView userscript URL globs");
        }
        List<String> patterns = new ArrayList<>(includes);
        patterns.addAll(excludes);
        Set<String> seen = new HashSet<>();
        for (String pattern : patterns) {
            if (GoText.utf8Length(pattern) > 512 || pattern.contains("#") || pattern.contains("\\")
                    || !URL_GLOB.matcher(pattern).matches() || !seen.add(pattern)) {
                throw new UserscriptException("invalid_url_glob", "expected an HTTP(S) URL glob with a literal lowercase host");
            }
        }
    }

    private static String replay(UserscriptRecord record, UserscriptCompatibilityReport metadata) {
        StringBuilder out = new StringBuilder(
            "(() => {\n const args = [");
        GoText.jsonQuote(out, record.id).append(',');
        GoText.jsonQuote(out, record.revision).append(',');
        GoText.jsonQuote(out, record.source).append(',');
        GoText.jsonArray(out, record.matches == null ? Collections.emptyList() : record.matches).append(',');
        GoText.jsonArray(out, record.excludeMatches == null ? Collections.emptyList() : record.excludeMatches).append(',');
        GoText.jsonArray(out, metadata.includes).append(',');
        GoText.jsonArray(out, metadata.excludes).append(',').append(!metadata.allFrames).append("];\n");
        return out.append(SCOPE).append(Userscript.REPLAY_BODY).toString();
    }

    // Use native Location fields and literal matching before touching page-overridable builtins.
    private static final String SCOPE =
        " const path = location.pathname + location.search;\n"
        + " const address = location.origin + path;\n"
        + " if ((args[7] && window.top !== window) || address.length > 8192) return;\n"
        + " const equalAt = (a, start, b, offset, count) => {\n"
        + "  if (start < 0 || start + count > a.length) return false;\n"
        + "  for (let i = 0; i < count; i++) if (a[start + i] !== b[offset + i]) return false;\n"
        + "  return true;\n"
        + " };\n"
        + " const glob = (pattern, value, p) => {\n"
        + "  let v = 0, star = -1, next = 0;\n"
        + "  while (v < value.length) {\n"
        + "   if (p < pattern.length && pattern[p] === '*') { star = p++; next = v; }\n"
        + "   else if (p < pattern.length && pattern[p] === value[v]) { p++; v++; }\n"
        + "   else if (star >= 0) { p = star + 1; v = ++next; }\n"
        + "   else return false;\n"
        + "  }\n"
        + "  while (p < pattern.length && pattern[p] === '*') p++;\n"
        + "  return p === pattern.length;\n"
        + " };\n"
        + " const match = pattern => {\n"
        + "  let colon = 0;\n"
        + "  while (pattern[colon] !== ':') colon++;\n"
        + "  const protocol = location.protocol, hostname = location.hostname;\n"
        + "  if (pattern[0] === '*') {\n"
        + "   if (protocol !== 'http:' && protocol !== 'https:') return false;\n"
        + "  } else if (protocol.length !== colon + 1 || !equalAt(protocol, 0, pattern, 0, colon + 1)) return false;\n"
        + "  const start = colon + 3;\n"
        + "  let slash = start;\n"
        + "  while (pattern[slash] !== '/') slash++;\n"
        + "  const count = slash - start;\n"
        + "  if (pattern[start] === '*' && pattern[start + 1] === '.') {\n"
        + "   const length = count - 2, offset = hostname.length - length;\n"
        + "   if ((offset !== 0 && hostname[offset - 1] !== '.') || !equalAt(hostname, offset, pattern, start + 2, length)) return false;\n"
        + "  } else if (!(count === 1 && pattern[start] === '*') &&\n"
        + "             (hostname.length !== count || !equalAt(hostname, 0, pattern, start, count))) return false;\n"
        + "  return glob(pattern, path, slash);\n"
        + " };\n"
        + " const any = (patterns, test) => {\n"
        + "  for (let i = 0; i < patterns.length; i++) if (test(patterns[i])) return true;\n"
        + "  return false;\n"
        + " };\n"
        + " const urlGlob = pattern => glob(pattern, address, 0);\n"
        + " if (!(any(args[3], match) || any(args[5], urlGlob)) || any(args[4], match) || any(args[6], urlGlob)) return;\n"
        + " const id = args[0], revision = args[1], source = args[2];\n";
}
