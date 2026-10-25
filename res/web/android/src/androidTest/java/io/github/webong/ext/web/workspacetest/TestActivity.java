package io.github.webong.ext.web.workspacetest;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.android.BrowserWorkspace;
import java.util.Arrays;
import java.util.Collections;

public final class TestActivity extends Activity {
    private BrowserWorkspace workspace;
    private int phase, passed, failed;
    private boolean evaluating, done, expectDocumentStart;

    @Override public void onCreate(Bundle saved) {
        super.onCreate(saved);
        expectDocumentStart = getIntent().getBooleanExtra("documentStart", true);
        try {
            workspace = new BrowserWorkspace(this, "test", new BrowserWorkspace.Listener() {
                @Override public void onStateChange(BrowserWorkspace.State state) {
                    if (!state.loading && state.progress == 1 && !evaluating && !done) {
                        evaluating = true;
                        workspace.view().evaluateJavascript("window.atStart", TestActivity.this::loaded);
                    }
                }
            });
            setContentView(workspace.view());
            check(workspace.supportsDocumentStartScripts() == expectDocumentStart, "document-start capability");
            try { workspace.navigate("file:///etc/passwd"); check(false, "file navigation rejected"); }
            catch (Exception expected) { check(true, "file navigation rejected"); }
            if (expectDocumentStart) {
                workspace.replaceUserscripts(Collections.singletonList(record()));
            } else {
                try {
                    workspace.replaceUserscripts(Collections.singletonList(record()));
                    check(false, "unsupported registration rejected");
                } catch (UnsupportedOperationException expected) { check(true, "unsupported registration rejected"); }
                workspace.replaceUserscripts(Collections.emptyList());
            }
            load("/public");
            new Handler(Looper.getMainLooper()).postDelayed(() -> {
                if (!done) { check(false, "test timeout"); finishTests(); }
            }, 30000);
        } catch (Exception failure) { fail(failure); }
    }

    private UserscriptRecord record() {
        return new UserscriptRecord("test", "script", "Script", "", Arrays.asList("https://example.com/*"),
            Arrays.asList("https://example.com/private/*"), true,
            "globalThis.runs = (globalThis.runs || 0) + 1;").signed();
    }
    private void load(String path) {
        evaluating = false;
        workspace.view().loadDataWithBaseURL("https://example.com" + path,
            "<html><script>window.atStart = window.runs || 0;</script></html>", "text/html", "UTF-8", null);
    }
    private void loaded(String value) {
        if (done) return;
        try {
            if (!expectDocumentStart) {
                check("0".equals(value), "navigation works without AndroidX");
                workspace.close();
                finishTests();
                return;
            }
            switch (phase++) {
                case 0:
                    check("1".equals(value), "runs before inline page code");
                    load("/private/secret"); break;
                case 1:
                    check("0".equals(value), "excluded path");
                    try {
                        workspace.replaceUserscripts(Collections.singletonList(record().withRevision("tampered")));
                        check(false, "tampered revision rejected");
                    } catch (Exception expected) { check(true, "tampered revision rejected"); }
                    load("/again"); break;
                case 2:
                    check("1".equals(value), "rejected update retains registrations");
                    workspace.replaceUserscripts(Collections.emptyList());
                    load("/removed"); break;
                default:
                    check("0".equals(value), "removed script stays removed on navigation");
                    workspace.close();
                    try { workspace.reload(); check(false, "closed workspace rejects work"); }
                    catch (IllegalStateException expected) { check(true, "closed workspace rejects work"); }
                    finishTests();
            }
        } catch (Exception failure) { fail(failure); }
    }
    private void check(boolean ok, String name) {
        if (ok) passed++; else failed++;
        Log.i("EXTWORKSPACE", (ok ? "PASS " : "FAIL ") + name);
    }
    private void fail(Exception failure) { Log.e("EXTWORKSPACE", "failure", failure); failed++; finishTests(); }
    private void finishTests() { done = true; Log.i("EXTWORKSPACE", "DONE passed=" + passed + " failed=" + failed); }
    @Override public void onDestroy() { if (workspace != null) workspace.close(); super.onDestroy(); }
}
