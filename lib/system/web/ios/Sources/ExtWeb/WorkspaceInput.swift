import Foundation

/// Mobile management input. Filesystem paths and remote debug endpoints are never accepted.
public struct WorkspaceInput {
    public var id: String = ""
    public var name: String = ""
    public var source: String = ""
    public var revision: String = ""
    public var url: String = ""
    public var matches: [String]?
    public var excludeMatches: [String]?
    public var records: [UserscriptRecord] = []
    public var selectedID: String = ""
    public var runAt: String = ""
    public var world: String = ""
    public static func decode(_ request: BrowserManagement.Request, target: String) throws -> Self {
        try request.validate()
        let text = request.input.flatMap { $0.isEmpty ? nil : $0 } ?? "{}"
        let m = try ManagementJSON.read(text, object: true)
        let allowed: Set<String> = request.kind == "userscript"
            ? ["id", "name", "source", "revision", "matches", "excludeMatches", "sessionTarget"]
            : ["target", "url", "source", "options", "records"]
        guard m.keys.allSatisfy({ allowed.contains($0) }) else { throw ManagementError.invalidRequest }
        var input = Self()
        input.id = try ManagementJSON.string(m["id"])
        input.name = try ManagementJSON.string(m["name"])
        input.source = try ManagementJSON.string(m["source"])
        input.revision = try ManagementJSON.string(m["revision"])
        input.url = try ManagementJSON.string(m["url"])
        if let raw = m["matches"] { input.matches = try JSONDecoder().decode([String]?.self, from: Data(raw.utf8)) }
        if let raw = m["excludeMatches"] { input.excludeMatches = try JSONDecoder().decode([String]?.self, from: Data(raw.utf8)) }
        if let raw = m["records"] { input.records = try JSONDecoder().decode([UserscriptRecord].self, from: Data(raw.utf8)) }
        if let raw = m[request.kind == "userscript" ? "sessionTarget" : "target"], raw != "null" {
            let selected = try ManagementJSON.read(raw, object: true)
            guard selected.keys.allSatisfy({ ["id", "protocol", "endpoint", "url", "title"].contains($0) }),
                  try ManagementJSON.string(selected["endpoint"]).isEmpty else { throw ManagementError.invalidRequest }
            _ = try ManagementJSON.string(selected["url"])
            _ = try ManagementJSON.string(selected["title"])
            let proto = try ManagementJSON.string(selected["protocol"])
            guard proto.isEmpty || proto == "webview" else { throw ManagementError.invalidRequest }
            input.selectedID = try ManagementJSON.string(selected["id"])
            guard input.selectedID.isEmpty || input.selectedID == target else { throw ManagementError.invalidRequest }
        }
        if let raw = m["options"], raw != "null" {
            let options = try ManagementJSON.read(raw, object: true)
            guard options.keys.allSatisfy({ ["runAt", "world"].contains($0) }) else { throw ManagementError.invalidRequest }
            input.runAt = try ManagementJSON.string(options["runAt"])
            input.world = try ManagementJSON.string(options["world"])
        }
        return input
    }
    public func validateInjection() throws {
        guard !source.isEmpty, source.utf8.count <= 1 << 20,
              runAt.isEmpty || runAt == "now", world.isEmpty || world == "page" else { throw ManagementError.invalidRequest }
    }
    public static func targetJSON(_ target: String, url: String = "", title: String = "") -> String {
        "{\"id\":\(GoJSON.quote(target)),\"protocol\":\"webview\",\"endpoint\":\"\",\"url\":\(GoJSON.quote(url)),\"title\":\(GoJSON.quote(title))}"
    }
}
