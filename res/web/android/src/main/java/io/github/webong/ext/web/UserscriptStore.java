package io.github.webong.ext.web;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

/** Go-compatible records in a host-owned private directory. The host excludes other-process
 * writers; all instances serialize operations in this process. Uses API-24-compatible file APIs. */
public final class UserscriptStore {
    public static final int MAX_RECORD_BYTES = 8 << 20;
    private static final Object LOCK = new Object();
    private final File directory;
    public UserscriptStore(File directory) throws IOException { this.directory = directory.getCanonicalFile(); }
    private File folder(String target) throws IOException {
        if (target == null || !GoText.isWellFormed(target) || target.isEmpty() || !target.equals(GoText.trimSpace(target)) || GoText.utf8Length(target) > 512)
            throw new IllegalArgumentException("invalid userscript target");
        try {
            byte[] hash = MessageDigest.getInstance("SHA-256").digest(target.getBytes(StandardCharsets.UTF_8));
            StringBuilder name = new StringBuilder();
            for (byte b : hash) name.append(String.format(java.util.Locale.ROOT, "%02x", b & 255));
            return new File(directory, name.toString());
        } catch (NoSuchAlgorithmException e) { throw new IllegalStateException("SHA-256 unavailable", e); }
    }
    private File path(String target, String id) throws IOException {
        if (id == null || !id.matches("[A-Za-z0-9][A-Za-z0-9._-]{0,127}")) throw new IllegalArgumentException("invalid userscript id");
        return new File(folder(target), id + ".json");
    }
    private static boolean check(File file, boolean folder, boolean missing) throws IOException {
        // Canonical checks reject live links. A dangling link still has a directory entry.
        if (!file.getCanonicalFile().equals(file.getAbsoluteFile())) throw new IOException("unsafe userscript path");
        if (!file.exists()) {
            File parent = file.getParentFile();
            String[] entries = parent == null ? null : parent.list();
            if (entries != null && Arrays.asList(entries).contains(file.getName())) throw new IOException("unsafe userscript path");
            if (missing) return false;
        }
        if (!(folder ? file.isDirectory() : file.isFile())) throw new IOException("missing or unsafe userscript file");
        return true;
    }
    public UserscriptRecord load(String target, String id) throws IOException, UserscriptException {
        synchronized (LOCK) {
            File file = path(target, id);
            check(directory, true, false); check(file.getParentFile(), true, false); check(file, false, false);
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            try (FileInputStream input = new FileInputStream(file)) {
                byte[] buffer = new byte[8192]; int count;
                while ((count = input.read(buffer)) >= 0) {
                    if (bytes.size() + count > MAX_RECORD_BYTES) throw new IOException("userscript record exceeds bound");
                    bytes.write(buffer, 0, count);
                }
            }
            String json = new String(bytes.toByteArray(), StandardCharsets.UTF_8);
            if (!Arrays.equals(bytes.toByteArray(), json.getBytes(StandardCharsets.UTF_8))) throw new IOException("invalid record UTF-8");
            ManagementJSON.read(json, true);
            UserscriptRecord record = UserscriptRecord.fromJson(json);
            if (!record.target.equals(target) || !record.id.equals(id)) throw new IOException("userscript identity mismatch");
            Userscript.validate(record); return record;
        }
    }
    public List<UserscriptRecord> list(String target) throws IOException, UserscriptException {
        synchronized (LOCK) {
            File folder = folder(target);
            List<UserscriptRecord> records = new ArrayList<>();
            if (!check(directory, true, true) || !check(folder, true, true)) return records;
            String[] names = folder.list(); if (names == null) throw new IOException("cannot list userscripts");
            Arrays.sort(names);
            for (String name : names) if (name.endsWith(".json")) records.add(load(target, name.substring(0, name.length() - 5)));
            return records;
        }
    }
    private static void privateDirectory(File folder) throws IOException {
        if (check(folder, true, true)) return;
        if (!folder.mkdirs() && !folder.isDirectory()) throw new IOException("cannot create userscript directory");
        if (!folder.setReadable(false, false) || !folder.setWritable(false, false) || !folder.setExecutable(false, false)
            || !folder.setReadable(true, true) || !folder.setWritable(true, true) || !folder.setExecutable(true, true))
            throw new IOException("cannot protect userscript directory");
    }
    public void save(UserscriptRecord record, boolean replace) throws IOException, UserscriptException {
        synchronized (LOCK) {
            Userscript.validate(record);
            File file = path(record.target, record.id);
            privateDirectory(directory); privateDirectory(file.getParentFile());
            boolean exists = check(file, false, true);
            if (exists != replace) throw new IOException(exists ? "userscript already installed" : "userscript not installed");
            byte[] bytes = record.toJson().getBytes(StandardCharsets.UTF_8);
            if (bytes.length > MAX_RECORD_BYTES) throw new IOException("userscript record exceeds bound");
            File temporary = File.createTempFile(".userscript-", ".tmp", file.getParentFile());
            try {
                if (!temporary.setReadable(false, false) || !temporary.setWritable(false, false)
                    || !temporary.setReadable(true, true) || !temporary.setWritable(true, true)) throw new IOException("cannot protect userscript record");
                try (FileOutputStream output = new FileOutputStream(temporary)) { output.write(bytes); output.getFD().sync(); }
                if (!temporary.renameTo(file)) throw new IOException("cannot atomically save userscript");
            } finally { if (temporary.exists() && !temporary.delete()) temporary.deleteOnExit(); }
        }
    }
    public UserscriptRecord setEnabled(String target, String id, boolean enabled) throws IOException, UserscriptException {
        synchronized (LOCK) {
            UserscriptRecord r = load(target, id);
            UserscriptRecord next = new UserscriptRecord(r.target, r.id, r.name, r.revision, r.matches, r.excludeMatches, enabled, r.source);
            save(next, true); return next;
        }
    }
    public void remove(String target, String id) throws IOException, UserscriptException {
        synchronized (LOCK) { load(target, id); if (!path(target, id).delete()) throw new IOException("cannot remove userscript"); }
    }
}
