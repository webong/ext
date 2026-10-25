package io.github.webong.ext.web.android;

import io.github.webong.ext.web.BrowserManagement;
import io.github.webong.ext.web.UserscriptManagement;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.UserscriptStore;
import io.github.webong.ext.web.WorkspaceInput;
import java.util.List;
import java.util.Objects;

/** Host-bound native management backend. All calls and completions use the UI thread.
 * Store operations are synchronous; use a small, private store. Does not own workspace lifetime. */
public final class WorkspaceManagement {
    public interface Completion { void complete(BrowserManagement.Response response, Exception error); }
    private final BrowserWorkspace workspace;
    private final UserscriptStore store;
    private final String profile;
    public WorkspaceManagement(BrowserWorkspace workspace, UserscriptStore store, String profile) {
        this.workspace = Objects.requireNonNull(workspace); this.store = Objects.requireNonNull(store); this.profile = Objects.requireNonNull(profile);
    }
    /** Synchronous validation failures throw before mutation. Injection completes asynchronously;
     * its execution failure is returned through completion. Completion is called at most once. */
    public void manage(String profile, BrowserManagement.Request request, Completion completion) throws Exception {
        Objects.requireNonNull(completion); workspace.checkOpen();
        if (!this.profile.equals(profile)) throw new IllegalArgumentException("workspace profile mismatch");
        request.validate();
        if (!request.kind.equals("session") && !request.kind.equals("userscript")) throw new UnsupportedOperationException("unsupported workspace operation");
        String target = workspace.managementTarget();
        WorkspaceInput input = WorkspaceInput.decode(request,target);
        if (request.kind.equals("userscript") && !request.action.equals("activate")) {
            completion.complete(UserscriptManagement.manage(request,target,store),null); return;
        }
        BrowserWorkspace.State state = workspace.state();
        String selected = WorkspaceInput.targetJson(target,state.url,state.title);
        String status = "ready", result;
        String operation = request.kind + "." + request.action;
        switch (operation) {
            case "session.targets": result = "[" + selected + "]"; break;
            case "session.connect": status = "connected"; result = selected; break;
            case "session.navigate": workspace.navigate(input.url); status = "navigated"; result = selected; break;
            case "session.inject":
                input.validateInjection();
                workspace.inject(input.source, ok -> {
                    if (!Boolean.TRUE.equals(ok)) { completion.complete(null,new IllegalStateException("workspace injection failed or workspace closed")); return; }
                    BrowserManagement.Response response = new BrowserManagement.Response(request,"injected",selected);
                    response.validate(request); completion.complete(response,null);
                }); return;
            case "session.replay": case "userscript.activate":
                List<UserscriptRecord> records = request.kind.equals("userscript") ? UserscriptManagement.activationRecords(input,target,store) : input.records;
                workspace.replaceUserscripts(records);
                int count = 0; for (UserscriptRecord record : records) if (record.enabled) count++;
                status = "activated";
                result = "{\"target\":" + selected + ",\"registered\":" + count + ",\"lifetime\":\"workspace\",\"scope\":\"future-documents\"}"; break;
            default: throw new UnsupportedOperationException("unsupported workspace operation");
        }
        BrowserManagement.Response response = new BrowserManagement.Response(request,status,result);
        response.validate(request); completion.complete(response,null);
    }
}
