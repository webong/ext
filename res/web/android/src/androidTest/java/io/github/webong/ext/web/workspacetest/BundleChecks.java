package io.github.webong.ext.web.workspacetest;

import android.app.Activity;
import io.github.webong.ext.web.android.BundleRunner;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.function.BiConsumer;

final class BundleChecks {
    private final Activity activity;
    private final BiConsumer<Boolean, String> check;
    private final Runnable finished;
    private File root;
    private BundleRunner runner;
    private int ready;
    private boolean moduleLog, hostLog, message;
    BundleChecks(Activity activity, BiConsumer<Boolean, String> check, Runnable finished) {
        this.activity = activity; this.check = check; this.finished = finished;
    }
    void start() {
        try {
            root = new File(activity.getCacheDir(), "bundle-" + java.util.UUID.randomUUID());
            if (!root.mkdirs()) throw new java.io.IOException("create fixture root");
            write("dep.mjs", "export const value = 7;");
            write("worker.js", "postMessage(42);");
            write(".secret", "private");
            write("main.mjs", "import {value} from './dep.mjs';\nconsole.info('module-loaded');\n"
                + "let blocked = false; try { await fetch('https://example.invalid/blocked'); } catch (_) { blocked = true; }\n"
                + "const hidden = await fetch('/.secret'); const worker = new Worker('/worker.js');\n"
                + "const answer = await new Promise(resolve => worker.onmessage = e => resolve(e.data)); worker.terminate();\n"
                + "ext.host.send(JSON.stringify({blocked, hidden:hidden.status, answer, transport:ext.host.transport}));\n"
                + "ext.host.onmessage = text => { console.log(text); ext.exit(text === 'host-message' && blocked && hidden.status === 404 && answer === 42 ? value : 99); };\n");
            BundleRunner.Options bad = new BundleRunner.Options(root); bad.entry = "../outside";
            try { new BundleRunner(activity, bad, null); check.accept(false, "bundle rejects escaping entry"); }
            catch (java.io.IOException expected) { check.accept(true, "bundle rejects escaping entry"); }
            BundleRunner.Options options = new BundleRunner.Options(root); options.entry = "main.mjs"; options.timeoutMillis = 15000;
            runner = new BundleRunner(activity, options, new BundleRunner.Listener() {
                @Override public void onReady() {
                    ready++;
                    try { runner.send("host-message"); } catch (Exception failure) { fail(failure); }
                }
                @Override public void onConsole(String level, String text) {
                    moduleLog |= text.equals("module-loaded"); hostLog |= text.equals("host-message");
                }
                @Override public void onMessage(String text) { message = text.contains("http") && text.contains("true"); }
            });
            activity.setContentView(runner.view());
            runner.run().thenAccept(result -> {
                check.accept(result.status.equals("exited") && result.exitCode == 7, "bundle module, worker, CSP and exit: " + result.status + "/" + result.exitCode);
                check.accept(ready == 1 && moduleLog && hostLog && message, "bundle console and bidirectional messages");
                try { runner.send("late"); check.accept(false, "bundle rejects late send"); }
                catch (Exception expected) { check.accept(true, "bundle rejects late send"); }
                try { runner.run(); check.accept(false, "bundle single-use"); }
                catch (IllegalStateException expected) { check.accept(true, "bundle single-use"); }
                new Thread(() -> {
                    try {
                        io.github.webong.ext.web.android.BundleServerChecks.run(root,
                            (ok, name) -> activity.runOnUiThread(() -> check.accept(ok, name)));
                        activity.runOnUiThread(this::timeout);
                    } catch (Exception failure) { activity.runOnUiThread(() -> fail(failure)); }
                }, "bundle-protocol-tests").start();
            });
        } catch (Exception failure) { fail(failure); }
    }
    private void timeout() {
        try {
            write("index.html", "<html><head><script>console.log('first-script'); ext.exit(0)</script></head></html>");
            BundleRunner.Options options = new BundleRunner.Options(root); options.timeoutMillis = 15000;
            boolean[] first = {false};
            runner = new BundleRunner(activity, options, new BundleRunner.Listener() {
                @Override public void onConsole(String level, String text) { first[0] |= text.equals("first-script"); }
            });
            activity.setContentView(runner.view());
            runner.run().thenAccept(result -> {
                check.accept(result.status.equals("exited") && first[0], "bundle HTML shim precedes first script");
                try {
                    write("index.html", "<head></head>");
                    options.timeoutMillis = 300;
                    runner = new BundleRunner(activity, options, null);
                    activity.setContentView(runner.view());
                    runner.run().thenAccept(timed -> {
                        check.accept(timed.status.equals("timeout"), "bundle overall timeout"); cancellation();
                    });
                } catch (Exception failure) { fail(failure); }
            });
        } catch (Exception failure) { fail(failure); }
    }
    private void cancellation() {
        try {
            runner = new BundleRunner(activity, new BundleRunner.Options(root), null);
            activity.setContentView(runner.view());
            java.util.concurrent.CompletableFuture<BundleRunner.Result> future = runner.run();
            runner.close(); runner.close();
            future.thenAccept(result -> {
                check.accept(result.status.equals("closed"), "bundle close is idempotent");
                try {
                    runner = new BundleRunner(activity, new BundleRunner.Options(root), null);
                    activity.setContentView(runner.view());
                    check.accept(runner.run().cancel(true), "bundle future cancellation");
                    try { runner.send("late"); check.accept(false, "cancelled bundle rejects send"); }
                    catch (java.io.IOException expected) { check.accept(true, "cancelled bundle rejects send"); }
                    cleanup(); finished.run();
                } catch (Exception failure) { fail(failure); }
            });
        } catch (Exception failure) { fail(failure); }
    }
    void close() { if (runner != null) runner.close(); cleanup(); }
    private void write(String name, String source) throws java.io.IOException {
        try (FileOutputStream out = new FileOutputStream(new File(root, name))) { out.write(source.getBytes(StandardCharsets.UTF_8)); }
    }
    private void cleanup() {
        if (root == null) return;
        File[] files = root.listFiles(); if (files != null) for (File file : files) file.delete(); root.delete();
    }
    private void fail(Exception failure) {
        android.util.Log.e("EXTWORKSPACE", "bundle test failure", failure);
        check.accept(false, "bundle exception"); close(); finished.run();
    }
}
