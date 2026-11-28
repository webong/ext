package io.github.webong.ext.web.workspacetest;

import android.app.Activity;
import android.os.Handler;
import android.os.Looper;
import io.github.webong.ext.web.UserscriptRecord;
import io.github.webong.ext.web.UserscriptStore;
import io.github.webong.ext.web.android.BrowserWorkspace;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.function.BiConsumer;

/** Real WebView checks: each store event must affect the next document, without reactivation. */
final class LiveUserscriptChecks {
    private interface Step { void run() throws Exception; }
    private final Activity activity;
    private final BiConsumer<Boolean,String> check;
    private final Runnable finished;
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final List<Step> steps = new ArrayList<>();
    private BrowserWorkspace workspace;
    private UserscriptStore store, writer;
    private File root;
    private Runnable afterChange;
    private boolean expectedSuccess, stopped;
    private int step, page, events;

    LiveUserscriptChecks(Activity activity, BiConsumer<Boolean,String> check, Runnable finished) {
        this.activity = activity; this.check = check; this.finished = finished;
    }
    private UserscriptRecord record(String id, int value) {
        return new UserscriptRecord("live", id, "Script", "", Collections.singletonList("https://example.com/*"),
            Collections.emptyList(), true, "globalThis.runs = " + value + ";").signed();
    }
    void start() {
        try {
            root = new File(activity.getCacheDir(), "live-" + UUID.randomUUID());
            store = new UserscriptStore(root); writer = new UserscriptStore(root);
            workspace = new BrowserWorkspace(activity, "live", new BrowserWorkspace.Listener() {
                @Override public void onUserscriptsReconciled(boolean success) {
                    events++;
                    check.accept(afterChange != null, "live callback belongs to active binding");
                    check.accept(success == expectedSuccess, "live reconciliation result");
                    Runnable next = afterChange; afterChange = null;
                    if (next != null) next.run();
                }
            });
            activity.setContentView(workspace.view());
            store.save(record("script",1), false);
            workspace.activateUserscripts(store, "");
            check.accept(workspace.isReconcilingUserscripts(), "workspace owns live binding");
            steps.add(() -> load(1));
            steps.add(() -> {
                expect(true,2);
                new Thread(() -> {
                    try { writer.save(record("script",2),true); }
                    catch (Exception e) { handler.post(() -> fail(e)); }
                }).start();
            });
            steps.add(() -> change(() -> writer.setEnabled("live","script",false), true,0));
            steps.add(() -> change(() -> writer.setEnabled("live","script",true), true,2));
            steps.add(() -> change(() -> writer.remove("live","script"), true,0));
            steps.add(() -> change(() -> writer.save(record("script",1),false), true,1));
            steps.add(() -> {
                workspace.activateUserscripts(store,"script");
                change(() -> writer.save(record("other",100),false),true,1);
            });
            steps.add(() -> change(() -> writer.setEnabled("live","script",false),true,0));
            steps.add(() -> change(() -> writer.remove("live","script"),true,0));
            steps.add(() -> change(() -> writer.save(record("script",1),false),true,1));
            steps.add(() -> {
                try { workspace.activateUserscripts(store,"missing"); check.accept(false,"failed rebind rejected"); }
                catch (IllegalArgumentException expected) { check.accept(true,"failed rebind rejected"); }
                try { workspace.replaceUserscripts(Collections.singletonList(record("script",1).withRevision("bad"))); check.accept(false,"invalid replay rejected"); }
                catch (Exception expected) { check.accept(true,"invalid replay rejected"); }
                check.accept(workspace.isReconcilingUserscripts(),"failed replacement retains binding");
                File[] folders = root.listFiles();
                if (folders == null || folders.length != 1) throw new IllegalStateException("fixture directory");
                try (FileOutputStream out = new FileOutputStream(new File(folders[0],"script.json"))) {
                    out.write("{}".getBytes(StandardCharsets.UTF_8));
                }
                change(() -> writer.save(record("other",100),true),false,1);
            });
            steps.add(() -> change(() -> writer.save(record("script",2),true),true,2));
            steps.add(() -> {
                int before = events;
                writer.save(record("script",1),true); // queued event must not override explicit replay
                workspace.replaceUserscripts(Collections.emptyList());
                check.accept(!workspace.isReconcilingUserscripts(),"explicit replay stops binding");
                handler.postDelayed(() -> {
                    check.accept(events == before,"stale queued event ignored");
                    load(0);
                },150);
            });
            steps.add(() -> {
                workspace.activateUserscripts(store,"script");
                workspace.stopUserscriptReconciliation();
                writer.setEnabled("live","script",false);
                handler.postDelayed(() -> load(1),150); // stopping retains existing registrations
            });
            steps.add(() -> {
                writer.setEnabled("live","script",true);
                workspace.activateUserscripts(store,"script");
                writer.save(record("script",2),true);
                int before = events;
                workspace.close();
                check.accept(!workspace.isReconcilingUserscripts(),"close detaches store");
                try { workspace.activateUserscripts(store,""); check.accept(false,"closed activation rejected"); }
                catch (IllegalStateException expected) { check.accept(true,"closed activation rejected"); }
                writer.save(record("script",1),true);
                handler.postDelayed(() -> {
                    check.accept(events == before,"closed workspace ignores queued and later changes");
                    finish();
                },150);
            });
            handler.postDelayed(() -> fail(new IllegalStateException("live test timeout")),20000);
            next();
        } catch (Exception e) { fail(e); }
    }
    private void expect(boolean success, int runs) {
        expectedSuccess = success; afterChange = () -> load(runs);
    }
    private void change(Step mutation, boolean success, int runs) throws Exception {
        expect(success,runs); mutation.run();
    }
    private void next() {
        if (stopped) return;
        try { steps.get(step++).run(); } catch (Exception e) { fail(e); }
    }
    private void load(int expected) {
        if (stopped) return;
        int id = ++page;
        workspace.view().loadDataWithBaseURL("https://example.com/live/" + id,
            "<html><script>window.pageID=" + id + ";window.atStart=window.runs||0;</script></html>","text/html","UTF-8",null);
        poll(id, expected, 0);
    }
    private void poll(int id, int expected, int attempt) {
        handler.postDelayed(() -> {
            if (stopped) return;
            workspace.view().evaluateJavascript("window.pageID + ':' + window.atStart", value -> {
                if (stopped) return;
                if (value.startsWith("\"" + id + ":")) {
                    check.accept(value.equals("\"" + id + ":" + expected + "\""),"live document-start result at step " + step);
                    next();
                } else if (attempt < 100) poll(id,expected,attempt+1);
                else fail(new IllegalStateException("live page did not load"));
            });
        },50);
    }
    private void fail(Exception e) {
        if (stopped) return;
        android.util.Log.e("EXTWORKSPACE","live reconciliation tests",e);
        check.accept(false,"live reconciliation suite"); finish();
    }
    private void finish() { close(); finished.run(); }
    void close() {
        if (stopped) return;
        stopped = true; handler.removeCallbacksAndMessages(null);
        if (workspace != null) workspace.close();
        if (root != null) remove(root);
    }
    private void remove(File file) {
        File[] children = file.listFiles(); if (children != null) for (File child : children) remove(child);
        file.delete();
    }
}
