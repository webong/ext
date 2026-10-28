package io.github.webong.ext.ml;

import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Stream;

/**
 * Bounded, caller-selected model artifact store. Bundles are stored under
 * {@code <root>/<provider>/<name>/<version>/} with a {@code manifest.json} and
 * artifact files. Installation stages, verifies SHA-256 digests, and
 * atomically publishes only after all checks pass.
 */
public final class MlStore {
    private final Path root;
    private final long maxTotalBytes;
    private final long maxArtifactBytes;
    private final int maxArtifacts;

    public MlStore(Path root) throws MlException {
        this(root, 1L << 40, 64L << 30, 64);
    }

    public MlStore(Path root, long maxTotalBytes, long maxArtifactBytes, int maxArtifacts)
            throws MlException {
        if (root == null || !root.isAbsolute()) {
            throw new MlException(MlException.Kind.INVALID, "model root must be absolute");
        }
        File rootFile = root.toFile();
        if (!rootFile.exists() || !rootFile.isDirectory()) {
            throw new MlException(MlException.Kind.INVALID, "model root must be an existing directory");
        }
        if (rootFile.canWrite() && rootFile.getUsableSpace() < 0) {
            throw new MlException(MlException.Kind.INVALID, "model root is not writable");
        }
        this.root = root.toAbsolutePath().normalize();
        this.maxTotalBytes = maxTotalBytes;
        this.maxArtifactBytes = maxArtifactBytes;
        this.maxArtifacts = maxArtifacts;
    }

    public Path root() { return root; }

    public MlInstalledModel install(MlManifest manifest, Map<String, ArtifactSource> files) throws MlException {
        manifest.validate();
        if (files.size() != manifest.artifacts.size()) {
            throw new MlException(MlException.Kind.INVALID, "artifact count mismatch");
        }
        MlReference reference = new MlReference(manifest.provider, manifest.name, manifest.version);
        Path targetDir = root.resolve(reference.provider).resolve(reference.name).resolve(reference.version);

        if (Files.exists(targetDir)) {
            throw new MlException(MlException.Kind.ALREADY_EXISTS, "model bundle already installed");
        }

        Path temporary = root.resolve(".ml-install-" + UUID.randomUUID());
        try {
            Files.createDirectories(temporary);
            long total = 0;
            for (MlArtifact artifact : manifest.artifacts) {
                ArtifactSource source = files.get(artifact.file);
                if (source == null) {
                    throw new MlException(MlException.Kind.INVALID, "missing artifact: " + artifact.file);
                }
                long declaredSize = source.size();
                if (declaredSize >= 0 && declaredSize > maxArtifactBytes) {
                    throw new MlException(MlException.Kind.INVALID, "artifact too large: " + artifact.file);
                }

                try (InputStream in = source.open()) {
                    if (in == null) {
                        throw new MlException(MlException.Kind.INVALID, "null stream for artifact: " + artifact.file);
                    }
                    Path artifactPath = temporary.resolve(artifact.file);
                    java.security.MessageDigest digest;
                    try {
                        digest = java.security.MessageDigest.getInstance("SHA-256");
                    } catch (java.security.NoSuchAlgorithmException e) {
                        throw new MlException(MlException.Kind.NATIVE_FAILURE, "SHA-256 unavailable", e);
                    }
                    long artifactBytes = 0;
                    byte[] buf = new byte[64 * 1024];
                    int read;
                    while ((read = in.read(buf)) >= 0) {
                        if (read == 0) continue;
                        digest.update(buf, 0, read);
                        artifactBytes += read;
                        if (artifactBytes > maxArtifactBytes) {
                            throw new MlException(MlException.Kind.INVALID, "artifact too large: " + artifact.file);
                        }
                        total += read;
                        if (total > maxTotalBytes) {
                            throw new MlException(MlException.Kind.INVALID, "bundle too large");
                        }
                        try (java.io.OutputStream out = new java.io.FileOutputStream(artifactPath.toFile(), true)) {
                            out.write(buf, 0, read);
                        }
                    }
                    String hexDigest = toHex(digest.digest());
                    if (!hexDigest.equals(artifact.sha256)) {
                        throw new MlException(MlException.Kind.DIGEST_MISMATCH, "digest mismatch: " + artifact.file);
                    }
                }
            }

            String manifestJson = new String(manifestToJson(manifest), java.nio.charset.StandardCharsets.UTF_8);
            Files.write(temporary.resolve("manifest.json"), manifestJson.getBytes(java.nio.charset.StandardCharsets.UTF_8));

            Path targetParent = targetDir.getParent();
            if (targetParent != null) Files.createDirectories(targetParent);
            Files.move(temporary, targetDir);
        } catch (MlException e) {
            try { Files.walk(temporary).sorted(java.util.Collections.reverseOrder()).forEach(p -> { try { Files.delete(p); } catch (Exception ignored) {} }); } catch (IOException ignored) {}
            throw e;
        } catch (IOException e) {
            try { Files.walk(temporary).sorted(java.util.Collections.reverseOrder()).forEach(p -> { try { Files.delete(p); } catch (Exception ignored) {} }); } catch (IOException ignored) {}
            throw new MlException(MlException.Kind.NATIVE_FAILURE, "stage model bundle: " + e.getMessage(), e);
        }

        return new MlInstalledModel(manifest, targetDir.toString());
    }

    /**
     * Convenience wrapper for small artifacts already in memory. Delegates to
     * {@link #install(MlManifest, Map)} by wrapping each byte array in an
     * {@link InMemoryArtifactSource}.
     */
    public MlInstalledModel installFromBytes(MlManifest manifest, Map<String, byte[]> files) throws MlException {
        java.util.Map<String, ArtifactSource> sources = new java.util.HashMap<>();
        for (java.util.Map.Entry<String, byte[]> entry : files.entrySet()) {
            sources.put(entry.getKey(), new InMemoryArtifactSource(entry.getValue()));
        }
        return install(manifest, sources);
    }

    private static String toHex(byte[] bytes) {
        StringBuilder sb = new StringBuilder(bytes.length * 2);
        for (byte b : bytes) sb.append(String.format("%02x", b));
        return sb.toString();
    }

    public MlInstalledModel resolve(MlReference reference) throws MlException {
        Path targetDir = root.resolve(reference.provider).resolve(reference.name).resolve(reference.version);
        Path manifestPath = targetDir.resolve("manifest.json");
        if (!Files.exists(manifestPath)) {
            throw new MlException(MlException.Kind.NOT_FOUND, "model not found");
        }
        byte[] manifestBytes;
        try {
            manifestBytes = Files.readAllBytes(manifestPath);
        } catch (IOException e) {
            throw new MlException(MlException.Kind.NATIVE_FAILURE, "read manifest: " + e.getMessage(), e);
        }
        if (manifestBytes.length > MlContract.MAX_MANIFEST_BYTES) {
            throw new MlException(MlException.Kind.INVALID, "manifest too large");
        }
        MlManifest manifest;
        try {
            manifest = parseManifest(new String(manifestBytes, java.nio.charset.StandardCharsets.UTF_8));
        } catch (Exception e) {
            throw new MlException(MlException.Kind.INVALID, "invalid manifest JSON: " + e.getMessage(), e);
        }
        if (!manifest.provider.equals(reference.provider) || !manifest.name.equals(reference.name)
                || !manifest.version.equals(reference.version)) {
            throw new MlException(MlException.Kind.INVALID, "manifest identity mismatch");
        }
        manifest.validate();
        for (MlArtifact artifact : manifest.artifacts) {
            Path artifactPath = targetDir.resolve(artifact.file);
            byte[] data;
            try {
                data = Files.readAllBytes(artifactPath);
            } catch (IOException e) {
                throw new MlException(MlException.Kind.NOT_FOUND, "read artifact: " + artifact.file, e);
            }
            if (!MlDigest.sha256(data).equals(artifact.sha256)) {
                throw new MlException(MlException.Kind.DIGEST_MISMATCH, "digest mismatch: " + artifact.file);
            }
        }
        return new MlInstalledModel(manifest, targetDir.toString());
    }

    public List<MlInstalledModel> list() throws MlException {
        List<MlInstalledModel> result = new ArrayList<>();
        try (Stream<Path> providers = Files.list(root)) {
            providers.filter(Files::isDirectory).forEach(providerDir -> {
                try (Stream<Path> names = Files.list(providerDir)) {
                    names.filter(Files::isDirectory).forEach(nameDir -> {
                        try (Stream<Path> versions = Files.list(nameDir)) {
                            versions.filter(Files::isDirectory).forEach(versionDir -> {
                                MlReference ref = new MlReference(
                                        providerDir.getFileName().toString(),
                                        nameDir.getFileName().toString(),
                                        versionDir.getFileName().toString());
                                try {
                                    result.add(resolve(ref));
                                } catch (MlException ignored) {}
                            });
                        } catch (IOException ignored) {}
                    });
                } catch (IOException ignored) {}
            });
        } catch (IOException e) {
            throw new MlException(MlException.Kind.NATIVE_FAILURE, "list models: " + e.getMessage(), e);
        }
        result.sort((a, b) -> (a.manifest.provider + "/" + a.manifest.name + "@" + a.manifest.version)
                .compareTo(b.manifest.provider + "/" + b.manifest.name + "@" + b.manifest.version));
        return result;
    }

    public void remove(MlReference reference) throws MlException {
        Path targetDir = root.resolve(reference.provider).resolve(reference.name).resolve(reference.version);
        if (!Files.exists(targetDir)) return;
        if (!Files.isDirectory(targetDir)) {
            throw new MlException(MlException.Kind.INVALID, "model bundle is not a directory");
        }
        try {
            Files.walk(targetDir).sorted(java.util.Collections.reverseOrder()).forEach(p -> {
                try { Files.delete(p); } catch (IOException ignored) {}
            });
        } catch (IOException e) {
            throw new MlException(MlException.Kind.NATIVE_FAILURE, "remove model bundle: " + e.getMessage(), e);
        }
        Path nameDir = targetDir.getParent();
        Path providerDir = nameDir == null ? null : nameDir.getParent();
        for (Path dir : new Path[]{nameDir, providerDir}) {
            if (dir == null || dir.equals(root)) continue;
            try (Stream<Path> entries = Files.list(dir)) {
                if (entries.findAny().isEmpty()) Files.delete(dir);
            } catch (IOException ignored) {}
        }
    }

    private byte[] manifestToJson(MlManifest manifest) {
        StringBuilder sb = new StringBuilder();
        sb.append("{\n");
        sb.append(String.format("  \"apiVersion\": \"%s\",\n", escapeJson(manifest.apiVersion)));
        sb.append(String.format("  \"name\": \"%s\",\n", escapeJson(manifest.name)));
        sb.append(String.format("  \"version\": \"%s\",\n", escapeJson(manifest.version)));
        sb.append(String.format("  \"provider\": \"%s\",\n", escapeJson(manifest.provider)));
        sb.append("  \"capabilities\": [");
        for (int i = 0; i < manifest.capabilities.size(); i++) {
            if (i > 0) sb.append(", ");
            sb.append(String.format("\"%s\"", escapeJson(manifest.capabilities.get(i))));
        }
        sb.append("],\n");
        sb.append("  \"artifacts\": [");
        for (int i = 0; i < manifest.artifacts.size(); i++) {
            MlArtifact a = manifest.artifacts.get(i);
            if (i > 0) sb.append(",");
            sb.append(String.format("\n    {\"role\": \"%s\", \"file\": \"%s\", \"sha256\": \"%s\"}",
                    escapeJson(a.role), escapeJson(a.file), escapeJson(a.sha256)));
        }
        if (!manifest.artifacts.isEmpty()) sb.append("\n  ");
        sb.append("]\n}\n");
        return sb.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8);
    }

    private static String escapeJson(String value) {
        return value.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t");
    }

    private static MlManifest parseManifest(String json) throws MlException {
        json = json.trim();
        if (!json.startsWith("{") || !json.endsWith("}")) {
            throw new MlException(MlException.Kind.INVALID, "manifest is not a JSON object");
        }
        java.util.Map<String, String> fields = new java.util.LinkedHashMap<>();
        String inner = json.substring(1, json.length() - 1).trim();
        int pos = 0;
        while (pos < inner.length()) {
            pos = skipWs(inner, pos);
            if (pos >= inner.length() || inner.charAt(pos) == '}') break;
            if (inner.charAt(pos) != '"') throw new MlException(MlException.Kind.INVALID, "expected string key");
            int keyStart = pos + 1;
            int keyEnd = inner.indexOf('"', keyStart);
            if (keyEnd < 0) throw new MlException(MlException.Kind.INVALID, "unterminated key");
            String key = inner.substring(keyStart, keyEnd);
            pos = keyEnd + 1;
            pos = skipWs(inner, pos);
            if (pos >= inner.length() || inner.charAt(pos) != ':') throw new MlException(MlException.Kind.INVALID, "expected colon");
            pos++;
            JsonValue value = parseJsonValue(inner, pos);
            fields.put(key, value.value);
            pos = value.end;
            pos = skipWs(inner, pos);
            if (pos < inner.length() && inner.charAt(pos) == ',') pos++;
        }
        java.util.List<MlArtifact> artifacts = new ArrayList<>();
        String artifactsJson = fields.get("artifacts");
        if (artifactsJson != null && !artifactsJson.equals("[]")) {
            // Parse artifacts array: [{...}, {...}]
            int aStart = artifactsJson.indexOf('[');
            int aEnd = artifactsJson.lastIndexOf(']');
            String aInner = artifactsJson.substring(aStart + 1, aEnd).trim();
            if (!aInner.isEmpty()) {
                int objStart = 0;
                int depth = 0;
                for (int i = 0; i < aInner.length(); i++) {
                    char c = aInner.charAt(i);
                    if (c == '{') { if (depth++ == 0) objStart = i; }
                    else if (c == '}') { if (--depth == 0) {
                        String obj = aInner.substring(objStart, i + 1);
                        java.util.Map<String, String> objFields = parseSimpleObject(obj);
                        artifacts.add(new MlArtifact(objFields.get("role"), objFields.get("file"), objFields.get("sha256")));
                    }}
                }
            }
        }
        return new MlManifest(fields.get("apiVersion"), fields.get("name"), fields.get("version"),
                fields.get("provider"), new ArrayList<>(), artifacts);
    }

    private static int skipWs(String s, int pos) {
        while (pos < s.length() && Character.isWhitespace(s.charAt(pos))) pos++;
        return pos;
    }

    private static class JsonValue {
        String value;
        int end;
        JsonValue(String value, int end) {
            this.value = value;
            this.end = end;
        }
    }

    private static JsonValue parseJsonValue(String s, int pos) throws MlException {
        pos = skipWs(s, pos);
        char c = s.charAt(pos);
        if (c == '"') {
            int end = pos + 1;
            while (end < s.length()) {
                char ch = s.charAt(end);
                if (ch == '\\') { end += 2; continue; }
                if (ch == '"') break;
                end++;
            }
            if (end >= s.length()) throw new MlException(MlException.Kind.INVALID, "unterminated string");
            String raw = s.substring(pos + 1, end);
            return new JsonValue(unescapeJson(raw), end + 1);
        }
        if (c == '[') {
            int depth = 0;
            for (int i = pos; i < s.length(); i++) {
                if (s.charAt(i) == '[') depth++;
                else if (s.charAt(i) == ']') { if (--depth == 0) return new JsonValue(s.substring(pos, i + 1), i + 1); }
            }
            throw new MlException(MlException.Kind.INVALID, "unterminated array");
        }
        throw new MlException(MlException.Kind.INVALID, "unsupported JSON value at " + pos);
    }

    private static java.util.Map<String, String> parseSimpleObject(String obj) throws MlException {
        java.util.Map<String, String> m = new java.util.LinkedHashMap<>();
        String inner = obj.substring(1, obj.length() - 1).trim();
        if (inner.isEmpty()) return m;
        int pos = 0;
        while (pos < inner.length()) {
            pos = skipWs(inner, pos);
            if (inner.charAt(pos) != '"') throw new MlException(MlException.Kind.INVALID, "expected key");
            int keyStart = pos + 1;
            int keyEnd = inner.indexOf('"', keyStart);
            if (keyEnd < 0) throw new MlException(MlException.Kind.INVALID, "unterminated key");
            String key = inner.substring(keyStart, keyEnd);
            pos = keyEnd + 1;
            pos = skipWs(inner, pos);
            if (pos >= inner.length() || inner.charAt(pos) != ':') throw new MlException(MlException.Kind.INVALID, "expected colon");
            pos++;
            JsonValue v = parseJsonValue(inner, pos);
            m.put(key, v.value);
            pos = v.end;
            pos = skipWs(inner, pos);
            if (pos < inner.length() && inner.charAt(pos) == ',') pos++;
        }
        return m;
    }

    private static String unescapeJson(String s) {
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (c == '\\' && i + 1 < s.length()) {
                char n = s.charAt(i + 1);
                switch (n) {
                    case '"': sb.append('"'); break;
                    case '\\': sb.append('\\'); break;
                    case '/': sb.append('/'); break;
                    case 'n': sb.append('\n'); break;
                    case 'r': sb.append('\r'); break;
                    case 't': sb.append('\t'); break;
                    default: sb.append(n); break;
                }
                i++;
            } else {
                sb.append(c);
            }
        }
        return sb.toString();
    }
}
