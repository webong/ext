package io.github.webong.ext.web;

import java.io.*;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.util.*;

/** Portable, unsigned WebExtension artifacts. Native installation/signing remain host-owned.
 * Source/output trees must be private and unchanged by other writers for each operation. */
public final class ExtensionArtifacts {
    private ExtensionArtifacts() {}
    static final int MAX_FILES = 2048, MAX_FILE = 16 << 20, MAX_TOTAL = 64 << 20, MAX_ZIP = 80 << 20;
    public static final class Description {
        public final String name, version, id, revision;
        public final int manifestVersion, files;
        public final long bytes;
        public final List<String> permissions, hostPermissions;
        Description(String name, String version, String id, int mv, List<String> p, List<String> h, int files, long bytes, String revision) {
            this.name=name; this.version=version; this.id=id; manifestVersion=mv;
            permissions=Collections.unmodifiableList(p); hostPermissions=Collections.unmodifiableList(h);
            this.files=files; this.bytes=bytes; this.revision=revision;
        }
    }
    static final class Item {
        String name; final byte[] data;
        Item(String name, byte[] data) { this.name=name; this.data=data; }
    }
    static IOException invalid() { return new IOException("invalid or unsafe extension artifact"); }
    static void safeName(String name) throws IOException {
        if (name.isEmpty() || name.indexOf('\\')>=0 || name.indexOf('\0')>=0 || name.indexOf(':')>=0 || name.startsWith("/")) throw invalid();
        for (String part:name.split("/",-1)) if (part.isEmpty() || part.equals(".") || part.equals("..")) throw invalid();
    }
    static byte[] read(File file, int limit) throws IOException {
        if (!file.isFile() || file.length()>limit) throw invalid();
        try (InputStream in=new FileInputStream(file); ByteArrayOutputStream out=new ByteArrayOutputStream()) {
            byte[] buffer=new byte[8192]; int n;
            while ((n=in.read(buffer))!=-1) { if (out.size()+n>limit) throw invalid(); out.write(buffer,0,n); }
            return out.toByteArray();
        }
    }
    private static File noLink(File input) throws IOException {
        File absolute=input.getAbsoluteFile(); File parent=absolute.getParentFile();
        File expected=parent==null?absolute:new File(parent.getCanonicalFile(),absolute.getName());
        if (!expected.equals(expected.getCanonicalFile())) throw invalid();
        return expected;
    }
    private static void walk(File root, String prefix, List<Item> files, long[] total, int depth, int[] count) throws IOException {
        if (depth>128 || ++count[0]>4096) throw invalid();
        File[] children=root.listFiles(); if (children==null) throw invalid();
        for (File child:children) {
            File file=noLink(child); String name=prefix+child.getName(); safeName(name);
            if (file.isDirectory()) walk(file,name+"/",files,total,depth+1,count);
            else {
                if (files.size()>=MAX_FILES || !file.isFile() || file.length()>MAX_FILE || total[0]+file.length()>MAX_TOTAL) throw invalid();
                byte[] data=read(file,MAX_FILE); total[0]+=data.length; if (total[0]>MAX_TOTAL) throw invalid(); files.add(new Item(name,data));
            }
        }
    }
    private static List<Item> source(File input) throws IOException {
        File root=noLink(input); List<Item> items;
        if (root.isDirectory()) { items=new ArrayList<>(); walk(root,"",items,new long[1],0,new int[1]); }
        else {
            String lower=root.getName().toLowerCase(Locale.ROOT);
            if (!(lower.endsWith(".zip") || lower.endsWith(".xpi"))) throw invalid();
            items=ExtensionZIP.decode(read(root,MAX_ZIP));
        }
        if (items.isEmpty() || items.size()>MAX_FILES) throw invalid();
        boolean manifest=false; for (Item item:items) manifest|=item.name.equals("manifest.json");
        if (!manifest) {
            String first=items.get(0).name; int slash=first.indexOf('/'); if (slash<0) throw invalid();
            String prefix=first.substring(0,slash+1);
            for (Item item:items) { if (!item.name.startsWith(prefix)) throw invalid(); item.name=item.name.substring(prefix.length()); }
        }
        items.sort((a,b)->utf8Compare(a.name,b.name));
        Set<String> names=new HashSet<>(); long total=0;
        for (Item item:items) {
            safeName(item.name); if (!names.add(item.name)) throw invalid();
            for (int at=item.name.indexOf('/');at>=0;at=item.name.indexOf('/',at+1)) if (names.contains(item.name.substring(0,at))) throw invalid();
            total+=item.data.length; if (item.data.length>MAX_FILE || total>MAX_TOTAL) throw invalid();
        }
        if (!names.contains("manifest.json")) throw invalid();
        return items;
    }
    static int utf8Compare(String a,String b) {
        byte[] x=a.getBytes(StandardCharsets.UTF_8), y=b.getBytes(StandardCharsets.UTF_8);
        for(int i=0;i<Math.min(x.length,y.length);i++) { int d=(x[i]&255)-(y[i]&255); if(d!=0)return d; }
        return x.length-y.length;
    }
    static MessageDigest sha() { try{return MessageDigest.getInstance("SHA-256");}catch(NoSuchAlgorithmException e){throw new IllegalStateException(e);} }
    static String hex(byte[] bytes) { StringBuilder out=new StringBuilder("sha256:");for(byte b:bytes)out.append(String.format(Locale.ROOT,"%02x",b&255));return out.toString(); }
    // java.util.Base64 requires Android 26; this library also runs on Android 24.
    private static byte[] decodeKey(String key) throws IOException {
        String text = key.replace("\r", "").replace("\n", "");
        if (text.length() % 4 != 0) throw invalid();
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        String alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
        for (int i = 0; i < text.length(); i += 4) {
            int a = alphabet.indexOf(text.charAt(i)), b = alphabet.indexOf(text.charAt(i + 1));
            char c = text.charAt(i + 2), d = text.charAt(i + 3);
            int x = alphabet.indexOf(c), y = alphabet.indexOf(d);
            if (a < 0 || b < 0 || (c == '=' && d != '=') ||
                (c != '=' && x < 0) || (d != '=' && y < 0) ||
                ((c == '=' || d == '=') && i + 4 != text.length())) throw invalid();
            out.write((a << 2) | (b >> 4));
            if (c != '=') out.write((b << 4) | (x >> 2));
            if (d != '=') out.write((x << 6) | y);
        }
        return out.toByteArray();
    }
    private static List<String> permissions(Map<String,Object> m,String key) {
        List<String> values=Json.strings(m,key); if(values==null)values=Collections.emptyList();
        if(values.size()>256)throw new IllegalArgumentException();
        Set<String> unique=new HashSet<>(values); unique.remove(""); List<String> sorted=new ArrayList<>(unique);sorted.sort(ExtensionArtifacts::utf8Compare);return sorted;
    }
    private static Description describe(List<Item> items) throws IOException {
        byte[] manifest=null;for(Item item:items)if(item.name.equals("manifest.json"))manifest=item.data;
        if(manifest==null || manifest.length>(1<<20))throw invalid();
        try {
            String text=new String(manifest,StandardCharsets.UTF_8);
            if(!Arrays.equals(manifest,text.getBytes(StandardCharsets.UTF_8)))throw invalid();
            ManagementJSON.read(text,true); Map<String,Object> m=Json.object(Json.parse(text),"manifest");
            long mv=Json.integer(m,"manifest_version");String name=Json.string(m,"name"), version=Json.string(m,"version"), key=Json.string(m,"key"), id="";
            if((mv!=2 && mv!=3)||GoText.trimSpace(name).isEmpty()||GoText.trimSpace(version).isEmpty()||name.getBytes(StandardCharsets.UTF_8).length>256||version.getBytes(StandardCharsets.UTF_8).length>64)throw invalid();
            if(!key.isEmpty()) {
                byte[] decoded=decodeKey(key);if(decoded.length==0)throw invalid();
                byte[] hash=sha().digest(decoded);StringBuilder value=new StringBuilder();
                for(int i=0;i<16;i++)value.append((char)('a'+((hash[i]&255)>>4))).append((char)('a'+(hash[i]&15)));id=value.toString();
            }
            MessageDigest hash=sha();long total=0;
            for(Item item:items) {
                hash.update(item.name.getBytes(StandardCharsets.UTF_8));hash.update((byte)0);
                long size=item.data.length;for(int shift=56;shift>=0;shift-=8)hash.update((byte)(size>>>shift));hash.update(item.data);total+=size;
            }
            return new Description(name,version,id,(int)mv,permissions(m,"permissions"),permissions(m,"host_permissions"),items.size(),total,hex(hash.digest()));
        } catch (RuntimeException error) { throw invalid(); }
    }
    public static Description inspect(File source) throws IOException { return describe(source(source)); }
    public static Description checkedSource(File source,String revision) throws IOException {
        if(revision==null||revision.isEmpty())throw invalid();Description d=inspect(source);if(!d.revision.equals(revision))throw new IOException("extension revision changed");return d;
    }
    public static void outsideSource(File source,File output) throws IOException {
        String input=source.getCanonicalPath(), target=output.getCanonicalPath();
        if(target.equals(input)||target.startsWith(input+File.separator))throw new IOException("output must be outside extension source");
    }
    private static boolean exists(File output) throws IOException {
        if(output.exists())return true;String[] entries=output.getParentFile().list();return entries!=null&&Arrays.asList(entries).contains(output.getName());
    }
    private static File output(File source,File destination) throws IOException {
        outsideSource(source,destination);File target=noLink(destination);
        if(exists(target))throw new IOException("output already exists");
        if(!target.getParentFile().isDirectory()&&!target.getParentFile().mkdirs())throw new IOException("cannot create output parent");return target;
    }
    public static void unusedArtifact(File destination,String suffix) throws IOException {
        int dot = destination.getName().lastIndexOf('.');
        if(!destination.isAbsolute() || dot < 0 || suffix == null || suffix.isEmpty() || !destination.getName().substring(dot).equalsIgnoreCase(suffix))throw invalid();
        File target=noLink(destination);if(exists(target))throw new IOException("output already exists");
        if(!target.getParentFile().isDirectory()&&!target.getParentFile().mkdirs())throw new IOException("cannot create output parent");
    }
    /** Deterministic uncompressed ZIP; source revision is identical to Go regardless of ZIP encoding. */
    public static Description packageArchive(File source,File destination) throws IOException {
        List<Item> items=source(source);Description d=describe(items);File target=output(source,destination);
        byte[] archive=ExtensionZIP.encode(items);if(!describe(ExtensionZIP.decode(archive)).revision.equals(d.revision))throw invalid();
        writeExclusive(target,archive);return d;
    }
    public static Description stage(File source,File destination) throws IOException {return stageWithRevision(source,destination,"");}
    public static Description stageWithRevision(File source,File destination,String expected) throws IOException {
        List<Item> items=source(source);Description d=describe(items);
        if(expected==null||(!expected.isEmpty()&&!expected.equals(d.revision)))throw new IOException("extension revision changed");
        File target=output(source,destination);if(!target.mkdir())throw new IOException("cannot exclusively create stage");boolean complete=false;
        try {
            for(Item item:items) {File file=new File(target,item.name);if(!file.getParentFile().isDirectory()&&!file.getParentFile().mkdirs())throw invalid();writeExclusive(file,item.data);}
            if(!inspect(target).revision.equals(d.revision))throw new IOException("staged revision differs");complete=true;return d;
        } finally {if(!complete)remove(target);}
    }
    static void writeExclusive(File output,byte[] data) throws IOException {
        if(!output.createNewFile())throw new IOException("output already exists");boolean complete=false;
        try {
            try(FileOutputStream stream=new FileOutputStream(output)){stream.write(data);stream.getFD().sync();}
            complete=true;
        }
        finally {if(!complete)output.delete();}
    }
    private static void remove(File file) {File[] children=file.listFiles();if(children!=null)for(File child:children)remove(child);file.delete();}
    public static void copyArtifactExclusive(File source,File destination) throws IOException {
        if(!source.isFile())throw invalid();if(!destination.createNewFile())throw new IOException("output already exists");boolean complete=false;
        try {
          try(InputStream in=new FileInputStream(source);FileOutputStream out=new FileOutputStream(destination)) {
            byte[] buffer=new byte[8192];int n;while((n=in.read(buffer))!=-1)out.write(buffer,0,n);out.getFD().sync();
          }
          complete=true;
        }finally {if(!complete)destination.delete();}
    }
    public static String fileSHA256(File file) throws IOException {
        if(!file.isFile())throw invalid();MessageDigest hash=sha();try(InputStream in=new FileInputStream(file)){byte[] buffer=new byte[8192];int n;while((n=in.read(buffer))!=-1)hash.update(buffer,0,n);}return hex(hash.digest());
    }
}
