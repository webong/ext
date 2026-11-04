import Foundation

/// Persistent workflows. Activation is explicit and handled by the native workspace router.
public enum UserscriptManagement {
    public static func descriptionJSON(_ r: UserscriptRecord, status: String = "") -> String {
        "{\"target\":\(GoJSON.quote(r.target)),\"id\":\(GoJSON.quote(r.id)),\"name\":\(GoJSON.quote(r.name)),\"revision\":\(GoJSON.quote(r.revision)),\"matches\":\(GoJSON.array(r.matches)),\"excludeMatches\":\(GoJSON.array(r.excludeMatches)),\"enabled\":\(r.enabled),\"bytes\":\(r.source.utf8.count),\"lifetime\":\"connected-session\"" + (status.isEmpty ? "" : ",\"status\":\(GoJSON.quote(status))") + "}"
    }
    public static func manage(_ request: BrowserManagement.Request, target: String, store: UserscriptStore) throws -> BrowserManagement.Response {
        guard request.kind == "userscript", request.action != "activate" else { throw ManagementError.invalidRequest }
        let input = try WorkspaceInput.decode(request, target: target)
        var status = "ready"
        let result: String
        switch request.action {
        case "prepare", "install", "update":
            var r = UserscriptRecord(target: target, id: input.id, name: input.name, matches: input.matches,
                excludeMatches: input.excludeMatches, enabled: true, source: input.source)
            r = Userscript.inferMetadata(r)
            r.revision = Userscript.revision(of: r)
            guard input.revision.isEmpty || input.revision == r.revision else { throw ManagementError.invalidRequest }
            try Userscript.validate(r)
            if request.action == "prepare" { status = "prepared" }
            else {
                if request.action == "update" { r.enabled = try store.load(target: target, id: r.id).enabled }
                try store.save(r, replace: request.action == "update")
                status = request.action == "update" ? "updated" : "installed"
            }
            result = descriptionJSON(r, status: status)
        case "list": result = "[" + (try store.list(target: target)).map { descriptionJSON($0) }.joined(separator: ",") + "]"
        case "describe": result = descriptionJSON(try store.load(target: target, id: input.id))
        case "enable", "disable":
            let r = try store.setEnabled(target: target, id: input.id, enabled: request.action == "enable")
            status = request.action + "d"; result = descriptionJSON(r)
        case "uninstall":
            try store.remove(target: target, id: input.id); status = "uninstalled"
            result = "{\"id\":\(GoJSON.quote(input.id)),\"target\":\(GoJSON.quote(target))}"
        default: throw ManagementError.invalidRequest
        }
        let response = BrowserManagement.Response(request: request, status: status, result: result)
        try response.validate(for: request); return response
    }
    public static func activationRecords(_ input: WorkspaceInput, target: String, store: UserscriptStore) throws -> [UserscriptRecord] {
        if !input.id.isEmpty {
            let record = try store.load(target: target, id: input.id)
            guard record.enabled else { throw ManagementError.invalidRequest }; return [record]
        }
        return try store.list(target: target).filter { $0.enabled }
    }
}
