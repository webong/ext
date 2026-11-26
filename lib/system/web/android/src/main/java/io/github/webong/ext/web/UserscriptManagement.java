package io.github.webong.ext.web;

import java.io.IOException;
import java.util.ArrayList;
import java.util.List;

/** Portable persistence workflows. Native activation is explicit. */
public final class UserscriptManagement {
    private UserscriptManagement() {}
    public static String descriptionJson(UserscriptRecord r, String status) {
        StringBuilder out = new StringBuilder("{\"target\":");
        GoText.jsonQuote(out,r.target).append(",\"id\":"); GoText.jsonQuote(out,r.id).append(",\"name\":");
        GoText.jsonQuote(out,r.name).append(",\"revision\":"); GoText.jsonQuote(out,r.revision).append(",\"matches\":");
        GoText.jsonArray(out,r.matches).append(",\"excludeMatches\":"); GoText.jsonArray(out,r.excludeMatches);
        out.append(",\"enabled\":").append(r.enabled).append(",\"bytes\":").append(GoText.utf8Length(r.source)).append(",\"lifetime\":\"connected-session\"");
        if (!status.isEmpty()) GoText.jsonQuote(out.append(",\"status\":"), status);
        return out.append('}').toString();
    }
    public static BrowserManagement.Response manage(BrowserManagement.Request request, String target, UserscriptStore store) throws IOException, UserscriptException {
        if (!request.kind.equals("userscript") || request.action.equals("activate")) throw new IllegalArgumentException("unsupported store operation");
        WorkspaceInput input = WorkspaceInput.decode(request, target);
        String status = "ready", result;
        switch (request.action) {
            case "prepare": case "install": case "update": {
                UserscriptRecord r = Userscript.inferMetadata(new UserscriptRecord(target,input.id,input.name,"",input.matches,input.excludeMatches,true,input.source), "").signed();
                if (!input.revision.isEmpty() && !input.revision.equals(r.revision)) throw new IllegalArgumentException("userscript changed since preparation");
                Userscript.validate(r);
                if (request.action.equals("prepare")) status = "prepared";
                else {
                    if (request.action.equals("update")) r = new UserscriptRecord(r.target,r.id,r.name,r.revision,r.matches,r.excludeMatches,store.load(target,r.id).enabled,r.source);
                    store.save(r, request.action.equals("update"));
                    status = request.action.equals("update") ? "updated" : "installed";
                }
                result = descriptionJson(r,status); break;
            }
            case "list": {
                StringBuilder results = new StringBuilder("[");
                for (UserscriptRecord r : store.list(target)) {
                    if (results.length() > 1) results.append(',');
                    results.append(descriptionJson(r,""));
                }
                result = results.append(']').toString(); break;
            }
            case "describe": result = descriptionJson(store.load(target,input.id), ""); break;
            case "enable": case "disable":
                result = descriptionJson(store.setEnabled(target,input.id,request.action.equals("enable")), ""); status = request.action + "d"; break;
            case "uninstall":
                store.remove(target,input.id); status = "uninstalled";
                result = "{\"id\":" + WorkspaceInput.quote(input.id) + ",\"target\":" + WorkspaceInput.quote(target) + "}"; break;
            default: throw new IllegalArgumentException("unsupported store operation");
        }
        BrowserManagement.Response response = new BrowserManagement.Response(request,status,result); response.validate(request); return response;
    }
    public static List<UserscriptRecord> activationRecords(WorkspaceInput input, String target, UserscriptStore store) throws IOException, UserscriptException {
        if (!input.id.isEmpty()) {
            UserscriptRecord r = store.load(target,input.id);
            if (!r.enabled) throw new IllegalArgumentException("userscript is disabled");
            return java.util.Collections.singletonList(r);
        }
        List<UserscriptRecord> records = new ArrayList<>();
        for (UserscriptRecord r : store.list(target)) if (r.enabled) records.add(r);
        return records;
    }
}
