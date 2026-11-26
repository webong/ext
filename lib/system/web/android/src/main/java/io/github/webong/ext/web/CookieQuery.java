package io.github.webong.ext.web;

import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;

/** Ordered cookie queries over host-authorized sources. Run get() off the UI thread.
 * Backends own discovery, authorization, source order and native-thread dispatch. */
public final class CookieQuery {
    private CookieQuery() {}
    public static final class Options {
        public String url = "", preferredSource = "", mode = "merge";
        public List<String> origins = new ArrayList<>(), names = new ArrayList<>(), sources = new ArrayList<>(), browsers = new ArrayList<>();
        public Map<String, String> profiles = new LinkedHashMap<>();
        /** JSON or Netscape input. Hosts read files/decode base64 before calling. */
        public byte[] inline, fallbackInline;
        public boolean inlineOnly, includeExpired, allowAllHosts;
        public long timeoutMillis;
        public Options() {}
        private Options(Options o) {
            url = o.url; preferredSource = o.preferredSource; mode = o.mode;
            origins = Collections.unmodifiableList(new ArrayList<>(o.origins)); names = Collections.unmodifiableList(new ArrayList<>(o.names));
            sources = Collections.unmodifiableList(new ArrayList<>(o.sources)); browsers = Collections.unmodifiableList(new ArrayList<>(o.browsers));
            profiles = Collections.unmodifiableMap(new LinkedHashMap<>(o.profiles));
            inline = o.inline == null ? null : o.inline.clone(); fallbackInline = o.fallbackInline == null ? null : o.fallbackInline.clone();
            inlineOnly = o.inlineOnly; includeExpired = o.includeExpired; allowAllHosts = o.allowAllHosts; timeoutMillis = o.timeoutMillis;
        }
    }
    public static final class Request {
        public final Site site;
        public final List<String> names;
        public final boolean includeExpired, allowAllHosts;
        Request(Site site, Options o) { this.site = site; names = o.names; includeExpired = o.includeExpired; allowAllHosts = site == null; }
    }
    public static final class Batch {
        public final List<Cookie> cookies;
        public final List<String> warnings;
        public final String storePath;
        public Batch(List<Cookie> cookies) { this(cookies, Collections.emptyList(), ""); }
        public Batch(List<Cookie> cookies, List<String> warnings, String storePath) {
            this.cookies = Collections.unmodifiableList(new ArrayList<>(cookies));
            this.warnings = Collections.unmodifiableList(new ArrayList<>(warnings)); this.storePath = storePath;
        }
    }
    public interface Query { Batch query(Context context, Request request) throws Exception; }
    public interface Legacy {
        List<Cookie> list(Context context, Site site) throws Exception;
        Cookie export(Context context, Site site, Cookie listed) throws Exception;
    }
    public static final class Source {
        public final String adapter, profile, label;
        private final Query query;
        private final Legacy legacy;
        public Source(String adapter, String profile, String label, Query query) {
            this.adapter = adapter; this.profile = profile; this.label = label; this.query = query; legacy = null;
        }
        public Source(String adapter, String profile, String label, Legacy legacy) {
            this.adapter = adapter; this.profile = profile; this.label = label; this.legacy = legacy; query = null;
        }
    }
    public static final class Selection {
        public final List<Source> sources;
        public final List<String> warnings;
        public Selection(List<Source> sources) { this(sources, Collections.emptyList()); }
        public Selection(List<Source> sources, List<String> warnings) {
            this.sources = Collections.unmodifiableList(new ArrayList<>(sources));
            this.warnings = Collections.unmodifiableList(new ArrayList<>(warnings));
        }
    }
    public interface Backend { Selection sources(Context context, Options options) throws Exception; }
    public static final class SourceInfo {
        public final String adapter, profile, storePath;
        public final boolean inline, fallback;
        SourceInfo(String adapter, String profile, String storePath, boolean inline, boolean fallback) {
            this.adapter = adapter; this.profile = profile; this.storePath = storePath; this.inline = inline; this.fallback = fallback;
        }
    }
    public static final class Entry {
        public final Cookie cookie;
        public final String source;
        public final SourceInfo sourceInfo;
        Entry(Cookie cookie, String source, SourceInfo info) { this.cookie = cookie; this.source = source; sourceInfo = info; }
        @Override public String toString() { return "CookieQuery.Entry(<redacted>)"; }
    }
    public static final class Result {
        public final List<Entry> cookies;
        public final List<String> warnings;
        Result(List<Entry> cookies, List<String> warnings) {
            this.cookies = Collections.unmodifiableList(new ArrayList<>(cookies));
            this.warnings = Collections.unmodifiableList(new ArrayList<>(warnings));
        }
        @Override public String toString() { return "CookieQuery.Result(" + cookies.size() + " cookies, " + warnings.size() + " warnings)"; }
    }
    public static final class Failure extends Exception {
        private static final long serialVersionUID = 1L;
        public enum Kind { INVALID_OPTIONS, BACKEND, INVALID_SOURCE, CANCELLED, TIMEOUT }
        public final Kind kind;
        public final Result partial;
        Failure(Kind kind, Result partial) { super("cookie query failed: " + kind); this.kind = kind; this.partial = partial; }
    }
    public static final class Cancellation {
        private final AtomicBoolean cancelled = new AtomicBoolean();
        public void cancel() { cancelled.set(true); }
    }
    /** Cooperative cancellation: backends must check this while doing blocking work. */
    public static final class Context {
        private final Cancellation cancellation;
        private final long began = System.nanoTime(), timeoutNanos;
        Context(Cancellation cancellation, long millis) { this.cancellation = cancellation; timeoutNanos = millis > Long.MAX_VALUE / 1_000_000 ? Long.MAX_VALUE : millis * 1_000_000; }
        public long remainingMillis() { return timeoutNanos == 0 ? Long.MAX_VALUE : Math.max(0, (timeoutNanos - (System.nanoTime() - began)) / 1_000_000); }
        public void check() throws Failure {
            if (cancellation.cancelled.get() || Thread.currentThread().isInterrupted()) throw new Failure(Failure.Kind.CANCELLED, empty());
            if (timeoutNanos > 0 && System.nanoTime() - began >= timeoutNanos) throw new Failure(Failure.Kind.TIMEOUT, empty());
        }
    }
    private static Result empty() { return new Result(Collections.emptyList(), Collections.emptyList()); }
    public static Result get(Options options, Backend backend) throws Exception { return get(options, backend, new Cancellation()); }
    public static Result get(Options options, Backend backend, Cancellation cancellation) throws Exception {
        Options o = new Options(options);
        if (!Arrays.asList("", "merge", "first").contains(o.mode) || o.timeoutMillis < 0
            || (!o.sources.isEmpty() && !o.browsers.isEmpty()) || (o.inlineOnly && (!o.sources.isEmpty() || !o.browsers.isEmpty())))
            throw new Failure(Failure.Kind.INVALID_OPTIONS, empty());
        Context context = new Context(Objects.requireNonNull(cancellation), o.timeoutMillis); context.check();
        List<Site> sites = new ArrayList<>();
        if (!o.url.isEmpty()) sites.add(Site.parse(o.url));
        for (String origin : o.origins) sites.add(Site.parse(origin));
        if (sites.isEmpty() && !o.allowAllHosts) throw new Failure(Failure.Kind.INVALID_OPTIONS, empty());
        List<Cookie> inline = o.inline == null ? Collections.emptyList() : CookieWorkflows.parse(o.inline);
        List<Cookie> fallback = o.fallbackInline == null ? Collections.emptyList() : CookieWorkflows.parse(o.fallbackInline);
        List<Entry> cookies = new ArrayList<>(); List<String> warnings = new ArrayList<>(); Set<List<Object>> seen = new HashSet<>();
        try {
            context.check(); append(inline, "inline", new SourceInfo("", "", "", true, false), sites, o, seen, cookies);
            if (o.mode.equals("first") && !cookies.isEmpty()) return new Result(cookies, warnings);
            if (!o.inlineOnly) {
                if (backend == null) throw new Failure(Failure.Kind.BACKEND, empty());
                Selection selected;
                try { selected = backend.sources(context, new Options(o)); }
                catch (InterruptedException cancelled) { Thread.currentThread().interrupt(); context.check(); throw new AssertionError("unreachable"); }
                catch (Exception error) { context.check(); throw new Failure(Failure.Kind.BACKEND, empty()); }
                if (selected == null) throw new Failure(Failure.Kind.INVALID_SOURCE, empty());
                warnings.addAll(selected.warnings); context.check();
                for (Source source : selected.sources) {
                    if (source == null || missing(source.adapter) || missing(source.profile) || missing(source.label)
                        || (source.query == null && source.legacy == null)) throw new Failure(Failure.Kind.INVALID_SOURCE, empty());
                    int before = cookies.size();
                    for (Site site : sites.isEmpty() ? Collections.<Site>singletonList(null) : sites) {
                        context.check();
                        try {
                            Batch batch = read(source, site, o, context); context.check();
                            List<Cookie> valid = new ArrayList<>(); List<String> rowWarnings = new ArrayList<>();
                            for (int index = 0; index < batch.cookies.size(); index++) {
                                try { Cookie cookie = batch.cookies.get(index); CookieRules.validate(cookie); valid.add(cookie); }
                                catch (Exception invalid) { rowWarnings.add(source.label + ": cookie " + (index + 1) + ": invalid cookie"); }
                            }
                            append(valid, source.label, new SourceInfo(source.adapter, source.profile, batch.storePath, false, false), sites, o, seen, cookies);
                            for (String warning : batch.warnings) warnings.add(source.label + ": " + warning);
                            warnings.addAll(rowWarnings);
                        } catch (InterruptedException cancelled) { Thread.currentThread().interrupt(); context.check(); }
                        catch (Exception failed) { context.check(); warnings.add(source.label + ": cookie source failed"); }
                    }
                    if (o.mode.equals("first") && cookies.size() > before) break;
                }
            }
            context.check();
            if (!o.mode.equals("first") || cookies.isEmpty()) append(fallback, "inline:fallback", new SourceInfo("", "", "", true, true), sites, o, seen, cookies);
            return new Result(cookies, warnings);
        } catch (Failure failed) { throw new Failure(failed.kind, new Result(cookies, warnings)); }
    }
    private static boolean missing(String value) { return value == null || value.isEmpty(); }
    private static boolean matches(Cookie c, List<Site> sites, Options o) {
        if (c.name.isEmpty() || c.domain.isEmpty() || !c.path.startsWith("/") || (!o.includeExpired && !CookieRules.isActive(c))
            || (!o.names.isEmpty() && !o.names.contains(c.name))) return false;
        if (sites.isEmpty()) return o.allowAllHosts;
        for (Site site : sites) if (CookieRules.domainMatches(site.hostname, c.domain)
            && (site.path.isEmpty() || CookieRules.pathMatches(site.escapedPath, c.path)) && (!c.secure || site.scheme.equals("https"))) return true;
        return false;
    }
    private static void append(List<Cookie> input, String label, SourceInfo info, List<Site> sites, Options o, Set<List<Object>> seen, List<Entry> output) {
        for (Cookie c : input) if (matches(c, sites, o)) {
            List<Object> key = Arrays.asList(c.name, c.domain, c.path, c.partitionKey, c.hasCrossSiteAncestor, c.attributes);
            if (seen.add(key)) output.add(new Entry(c, label, info));
        }
    }
    private static Batch read(Source s, Site site, Options o, Context context) throws Exception {
        if (s.query != null) return s.query.query(context, new Request(site, o));
        if (site == null || o.includeExpired) return new Batch(Collections.emptyList(),
            Collections.singletonList("source does not support all-host or expired-cookie queries"), "");
        List<Cookie> listed = s.legacy.list(context, site); context.check();
        List<Cookie> cookies = new ArrayList<>(); List<String> warnings = new ArrayList<>();
        for (Cookie cookie : listed) if ((o.names.isEmpty() || o.names.contains(cookie.name)) && CookieRules.matches(site, cookie, false)) {
            context.check();
            try {
                Cookie full = s.legacy.export(context, site, cookie); context.check(); CookieRules.validate(full);
                if (!CookieRules.sameListed(cookie, full)) { warnings.add("export returned a different cookie"); continue; }
                cookies.add(full);
            } catch (InterruptedException cancelled) { Thread.currentThread().interrupt(); context.check(); }
            catch (Exception failure) { context.check(); warnings.add("cookie export failed"); }
        }
        return new Batch(cookies, warnings, "");
    }
}
