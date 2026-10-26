package io.github.webong.ext.web;

import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.charset.StandardCharsets;
import java.util.List;

final class UserscriptStoreTests {
    private UserscriptStoreTests() {}
    private interface Action { void run() throws Exception; }
    private static void rejects(Action action, String message) throws Exception {
        try { action.run(); WebTests.check(false,message); }
        catch (java.io.IOException | UserscriptException | IllegalArgumentException expected) { WebTests.check(true,message); }
    }
    static void run() throws Exception {
        Path root = Files.createTempDirectory("ext-userscript-store-");
        try {
            String fixtures = System.getenv("EXT_WEB_TESTDATA");
            UserscriptRecord r = UserscriptRecord.fromJson(Files.readString(Paths.get(fixtures == null ? "res/web/testdata" : fixtures,"userscript/store/record.json")));
            UserscriptStore store = new UserscriptStore(root.toFile());
            UserscriptStore writer = new UserscriptStore(root.toFile());
            int[] events = {0};
            UserscriptStore.Observation observation = store.observe(r.target, () -> {
                events[0]++;
                Thread reader = new Thread(() -> { try { writer.list(r.target); } catch (Exception e) { throw new AssertionError(e); } });
                reader.start();
                try { reader.join(2000); } catch (InterruptedException e) { throw new AssertionError(e); }
                WebTests.check(!reader.isAlive(), "store observers run outside lock");
            });
            try {
                writer.save(r,false); WebTests.eq(1,events[0],"cross-instance event");
                rejects(() -> writer.save(r,false),"failed mutation has no event");
                WebTests.eq(1,events[0],"failed mutation event count");
                writer.setEnabled(r.target,r.id,false); WebTests.eq(2,events[0],"enable event once");
                writer.remove(r.target,r.id); WebTests.eq(3,events[0],"remove event");
                observation.close();
                writer.save(r,false); WebTests.eq(3,events[0],"cancel observer");
                writer.remove(r.target,r.id);
            } finally { observation.close(); }
            WebTests.eq(0,store.list(r.target).size(),"empty store");
            rejects(() -> store.save(r,true),"update missing"); store.save(r,false);
            WebTests.eq(r,new UserscriptStore(root.toFile()).load(r.target,r.id),"Go fixture round trip after reopen");
            rejects(() -> store.save(r,false),"duplicate install");
            WebTests.eq(0,store.list("other").size(),"target isolation");
            rejects(() -> store.load("other",r.id),"target mismatch");
            UserscriptRecord disabled = store.setEnabled(r.target,r.id,false);
            WebTests.eq(r.revision,disabled.revision,"enable flag does not change revision");
            rejects(() -> store.save(r.withRevision("tampered"),true),"revision rejected");
            WebTests.eq(disabled,store.load(r.target,r.id),"rejected write preserves record");
            rejects(() -> store.load(r.target,"../outside"),"traversal rejected");
            rejects(() -> store.list(" "),"blank target rejected");
            Path folder;
            try (java.util.stream.Stream<Path> children = Files.list(root)) { folder = children.findFirst().orElseThrow(); }
            WebTests.eq("edcb219f80355327e74351977daf7a16144d20773871f784ab3d5c725acc7ad0",folder.getFileName().toString(),"Go target directory hash");
            Path file = folder.resolve(r.id + ".json");
            WebTests.eq(java.nio.file.attribute.PosixFilePermissions.fromString("rw-------"),Files.getPosixFilePermissions(file),"private permissions");
            try (java.io.RandomAccessFile oversized = new java.io.RandomAccessFile(file.toFile(),"rw")) { oversized.setLength(UserscriptStore.MAX_RECORD_BYTES + 1L); }
            rejects(() -> store.load(r.target,r.id),"oversized record rejected");
            Files.writeString(file,"{}");
            rejects(() -> store.list(r.target),"corruption rejected");
            Files.delete(file);
            Path outside = root.resolve("outside"); Files.writeString(outside,r.toJson());
            Files.createSymbolicLink(file,outside);
            rejects(() -> store.load(r.target,r.id),"symlink read rejected");
            rejects(() -> store.save(r,true),"symlink update rejected");
            rejects(() -> store.remove(r.target,r.id),"symlink removal rejected");
            Files.delete(file); Files.createSymbolicLink(file,root.resolve("missing"));
            rejects(() -> store.save(r,false),"dangling symlink rejected");
            Files.delete(file); store.save(r,false); store.remove(r.target,r.id);
            rejects(() -> store.load(r.target,r.id),"removed");
            String input = "{\"id\":\"script\",\"name\":\"Script\",\"matches\":[\"https://example.com/*\"],\"source\":\"window.fixture = 1;\"}";
            BrowserManagement.Response prepared = manage(store,"prepare",input);
            WebTests.eq("prepared",prepared.status,"prepare");
            WebTests.check(!prepared.result.contains("window.fixture"),"description omits source");
            WebTests.eq(0,store.list("test").size(),"prepare does not persist");
            WebTests.eq("installed",manage(store,"install",input).status,"install");
            rejects(() -> manage(store,"install",input),"duplicate install");
            WebTests.eq("disabled",manage(store,"disable","{\"id\":\"script\"}").status,"disable");
            WebTests.eq("updated",manage(store,"update",input.replace("= 1","= 2")).status,"update");
            WebTests.check(!store.load("test","script").enabled,"update preserves disabled");
            rejects(() -> manage(store,"update",input.substring(0,input.length()-1) + ",\"revision\":\"stale\"}"),"stale review");
            WebTests.eq("enabled",manage(store,"enable","{\"id\":\"script\"}").status,"enable");
            WebTests.eq("uninstalled",manage(store,"uninstall","{\"id\":\"script\"}").status,"uninstall");
            rejects(() -> manage(store,"describe","{\"id\":\"script\"}"),"describe missing");
            rejects(() -> manage(store,"install","{\"sourceFile\":\"/etc/passwd\"}"),"sourceFile unsupported");
            rejects(() -> WorkspaceInput.decode(new BrowserManagement.Request("session","navigate","{\"target\":{\"id\":\"other\"}}"),"test"),"selected target mismatch");
            rejects(() -> WorkspaceInput.validateSource(String.valueOf((char) 0xd800)),"malformed native injection source");
            WorkspaceInput.decode(new BrowserManagement.Request("session","connect","{\"target\":" + WorkspaceInput.targetJson("test","https://example.com","Page") + "}"),"test");
            rejects(() -> WorkspaceInput.decode(new BrowserManagement.Request("session","inject","{\"source\":\"1\",\"options\":{\"runAt\":\"document-start\"}}"),"test").validateInjection(),"unsupported injection timing");
        } finally {
            try (java.util.stream.Stream<Path> paths = Files.walk(root)) {
                for (Path path : (Iterable<Path>) paths.sorted(java.util.Comparator.reverseOrder())::iterator) Files.delete(path);
            }
        }
    }
    private static BrowserManagement.Response manage(UserscriptStore store, String action, String input) throws Exception {
        return UserscriptManagement.manage(new BrowserManagement.Request("userscript",action,input),"test",store);
    }
}
