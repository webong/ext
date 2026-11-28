import Foundation
import ExtWeb

/// A management backend bound by the host to one profile, workspace and private script store.
/// Owns neither the workspace lifetime nor UI. Call on the main actor.
@MainActor
public final class WorkspaceManagement {
    private let workspace: BrowserWorkspace
    private let store: UserscriptStore
    private let profile: String
    public init(workspace: BrowserWorkspace, store: UserscriptStore, profile: String) {
        self.workspace = workspace; self.store = store; self.profile = profile
    }
    public func manage(profile: String, request: BrowserManagement.Request) async throws -> BrowserManagement.Response {
        try workspace.checkOpen()
        guard profile == self.profile else { throw ManagementError.invalidRequest }
        try request.validate()
        guard request.kind == "session" || request.kind == "userscript" else { throw ManagementError.invalidRequest }
        let input = try WorkspaceInput.decode(request, target: workspace.target)
        if request.kind == "userscript", request.action != "activate" {
            return try UserscriptManagement.manage(request, target: workspace.target, store: store)
        }
        var status = "ready"
        let result: String
        let state = workspace.state
        let selected = WorkspaceInput.targetJSON(workspace.target, url: state.url, title: state.title)
        switch (request.kind, request.action) {
        case ("session", "targets"): result = "[" + selected + "]"
        case ("session", "connect"): status = "connected"; result = selected
        case ("session", "navigate"):
            try workspace.navigate(input.url); status = "navigated"; result = selected
        case ("session", "inject"):
            try input.validateInjection()
            try await workspace.inject(input.source)
            status = "injected"; result = selected
        case ("session", "replay"), ("userscript", "activate"):
            let count: Int
            if request.kind == "userscript" {
                count = try workspace.activateUserscripts(store: store, id: input.id)
            } else {
                try workspace.replaceUserscripts(input.records)
                count = input.records.filter { $0.enabled }.count
            }
            status = "activated"
            result = "{\"target\":\(selected),\"registered\":\(count),\"lifetime\":\"workspace\",\"scope\":\"future-documents\"}"
        default: throw ManagementError.invalidRequest
        }
        let response = BrowserManagement.Response(request: request, status: status, result: result)
        try response.validate(for: request); return response
    }
}
