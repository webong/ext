import Foundation

/// Ordered, host-authorized cookie queries. This library never discovers or selects a browser.
public enum CookieQuery {
    public struct Options {
        public var url = "", origins: [String] = [], names: [String] = []
        public var sources: [String] = [], browsers: [String] = [], profiles: [String: String] = [:]
        public var preferredSource = "", mode = "merge"
        /// JSON or Netscape data, bounded by CookieWorkflows. Host reads files/decodes base64.
        public var inline: Data?, fallbackInline: Data?
        public var inlineOnly = false, includeExpired = false, allowAllHosts = false
        public var timeout: TimeInterval = 0
        public init() {}
    }
    public struct Request {
        public let site: Site?
        public let names: [String]
        public let includeExpired: Bool
        public var allowAllHosts: Bool { site == nil }
    }
    public struct Batch {
        public var cookies: [Cookie], warnings: [String], storePath: String
        public init(cookies: [Cookie], warnings: [String] = [], storePath: String = "") {
            self.cookies = cookies; self.warnings = warnings; self.storePath = storePath
        }
    }
    public struct Source {
        public let adapter: String, profile: String, label: String
        public var query: ((Context, Request) async throws -> Batch)?
        public var list: ((Context, Site) async throws -> [Cookie])?
        public var export: ((Context, Site, Cookie) async throws -> Cookie)?
        public init(adapter: String, profile: String, label: String,
                    query: @escaping (Context, Request) async throws -> Batch) {
            self.adapter = adapter; self.profile = profile; self.label = label; self.query = query
        }
        public init(adapter: String, profile: String, label: String,
                    list: @escaping (Context, Site) async throws -> [Cookie],
                    export: @escaping (Context, Site, Cookie) async throws -> Cookie) {
            self.adapter = adapter; self.profile = profile; self.label = label; self.list = list; self.export = export
        }
    }
    public struct Selection {
        public let sources: [Source], warnings: [String]
        public init(sources: [Source], warnings: [String] = []) { self.sources = sources; self.warnings = warnings }
    }
    public typealias Backend = (Context, Options) async throws -> Selection
    public struct SourceInfo: Equatable {
        public let adapter: String, profile: String, storePath: String
        public let inline: Bool, fallback: Bool
    }
    public struct Entry: CustomStringConvertible {
        public let cookie: Cookie, source: String, sourceInfo: SourceInfo
        public var description: String { "CookieQuery.Entry(<redacted>)" }
    }
    public struct Result: CustomStringConvertible {
        public var cookies: [Entry] = [], warnings: [String] = []
        public init() {}
        public var description: String { "CookieQuery.Result(\(cookies.count) cookies, \(warnings.count) warnings)" }
    }
    public struct Failure: Error, CustomStringConvertible {
        public enum Kind { case invalidOptions, backend, invalidSource, cancelled, timeout }
        public let kind: Kind, partial: Result
        public var description: String { "cookie query failed: \(kind)" }
    }
    /// Deadlines/cancellation are cooperative, including while a host backend is suspended.
    public struct Context {
        public let deadlineUptime: TimeInterval?
        public func check() throws {
            if Task.isCancelled { throw Failure(kind: .cancelled, partial: Result()) }
            if let deadlineUptime, ProcessInfo.processInfo.systemUptime >= deadlineUptime {
                throw Failure(kind: .timeout, partial: Result())
            }
        }
    }
    private struct Identity: Hashable {
        struct Attribute: Hashable { let key: Data, value: Data }
        let fields: [Data]
        let ancestor: Bool
        let attributes: [Attribute]
        init(_ c: Cookie) {
            fields = [c.name, c.domain, c.path, c.partitionKey].map { Data($0.utf8) }
            ancestor = c.hasCrossSiteAncestor
            attributes = c.attributes.map { Attribute(key: Data($0.key.utf8), value: Data($0.value.utf8)) }
                .sorted { $0.key.lexicographicallyPrecedes($1.key) }
        }
    }
    private static func named(_ name: String, in names: [String]) -> Bool {
        names.isEmpty || names.contains { $0.utf8.elementsEqual(name.utf8) }
    }
    public static func get(_ options: Options, backend: Backend? = nil) async throws -> Result {
        guard ["", "merge", "first"].contains(options.mode), options.timeout.isFinite, options.timeout >= 0,
              options.sources.isEmpty || options.browsers.isEmpty,
              !options.inlineOnly || (options.sources.isEmpty && options.browsers.isEmpty) else {
            throw Failure(kind: .invalidOptions, partial: Result())
        }
        let context = Context(deadlineUptime: options.timeout > 0 ? ProcessInfo.processInfo.systemUptime + options.timeout : nil)
        try context.check()
        let raw = (options.url.isEmpty ? [] : [options.url]) + options.origins
        guard !raw.isEmpty || options.allowAllHosts else { throw Failure(kind: .invalidOptions, partial: Result()) }
        let sites = try raw.map(parseSite)
        // Parse both inputs before selecting sources, even when inline wins in first mode.
        let inline = try options.inline.map(CookieWorkflows.parse) ?? []
        let fallback = try options.fallbackInline.map(CookieWorkflows.parse) ?? []
        var result = Result(), seen = Set<Identity>()
        func check() throws {
            do { try context.check() }
            catch let failure as Failure { throw Failure(kind: failure.kind, partial: result) }
        }
        func append(_ cookies: [Cookie], label: String, info: SourceInfo) {
            for cookie in cookies where matches(cookie, sites, options) {
                if seen.insert(Identity(cookie)).inserted { result.cookies.append(Entry(cookie: cookie, source: label, sourceInfo: info)) }
            }
        }
        func appendInline(_ cookies: [Cookie], fallback: Bool) {
            append(cookies, label: fallback ? "inline:fallback" : "inline",
                   info: SourceInfo(adapter: "", profile: "", storePath: "", inline: true, fallback: fallback))
        }
        try check(); appendInline(inline, fallback: false)
        if options.mode == "first" && !result.cookies.isEmpty { return result }
        if !options.inlineOnly {
            guard let backend else { throw Failure(kind: .backend, partial: result) }
            let selection: Selection
            do { selection = try await backend(context, options) }
            catch { try check(); throw Failure(kind: .backend, partial: result) }
            result.warnings += selection.warnings; try check()
            for source in selection.sources {
                guard !source.adapter.isEmpty, !source.profile.isEmpty, !source.label.isEmpty,
                      source.query != nil || (source.list != nil && source.export != nil) else {
                    throw Failure(kind: .invalidSource, partial: result)
                }
                let before = result.cookies.count
                for site in sites.isEmpty ? [Site?.none] : sites.map(Optional.some) {
                    try check()
                    do {
                        let batch = try await read(source, site, options, context)
                        try check()
                        var valid: [Cookie] = [], rowWarnings: [String] = []
                        for (index, cookie) in batch.cookies.enumerated() {
                            do { try CookieRules.validate(cookie); valid.append(cookie) }
                            catch { rowWarnings.append("cookie \(index + 1): invalid cookie") }
                        }
                        append(valid, label: source.label, info: SourceInfo(adapter: source.adapter, profile: source.profile,
                            storePath: batch.storePath, inline: false, fallback: false))
                        result.warnings += (batch.warnings + rowWarnings).map { source.label + ": " + $0 }
                    } catch {
                        try check()
                        result.warnings.append(source.label + ": cookie source failed")
                    }
                }
                if options.mode == "first" && result.cookies.count > before { break }
            }
        }
        try check()
        if options.mode != "first" || result.cookies.isEmpty { appendInline(fallback, fallback: true) }
        return result
    }
    private static func matches(_ c: Cookie, _ sites: [Site], _ o: Options) -> Bool {
        guard !c.name.isEmpty, !c.domain.isEmpty, c.path.hasPrefix("/"),
              o.includeExpired || CookieRules.isActive(c), named(c.name, in: o.names) else { return false }
        if sites.isEmpty { return o.allowAllHosts }
        return sites.contains { CookieRules.domainMatches(siteHost: $0.hostname, cookieDomain: c.domain) &&
            ($0.path.isEmpty || CookieRules.pathMatches(requestPath: $0.escapedPath, cookiePath: c.path)) && (!c.secure || $0.scheme == "https") }
    }
    private static func read(_ source: Source, _ site: Site?, _ o: Options, _ context: Context) async throws -> Batch {
        if let query = source.query { return try await query(context, Request(site: site, names: o.names, includeExpired: o.includeExpired)) }
        guard let site, !o.includeExpired, let list = source.list, let export = source.export else {
            return Batch(cookies: [], warnings: ["source does not support all-host or expired-cookie queries"])
        }
        let listed = try await list(context, site); try context.check()
        var batch = Batch(cookies: [])
        for cookie in listed where named(cookie.name, in: o.names) && CookieRules.matches(site, cookie) {
            try context.check()
            do {
                let full = try await export(context, site, cookie); try context.check()
                try CookieRules.validate(full)
                guard Identity(cookie) == Identity(full), cookie.id == full.id, cookie.ref.utf8.elementsEqual(full.ref.utf8) else { batch.warnings.append("export returned a different cookie"); continue }
                batch.cookies.append(full)
            } catch { try context.check(); batch.warnings.append("cookie export failed") }
        }
        return batch
    }
}
