package io.github.webong.ext.ml;

import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.List;
import java.util.Map;

public class MlTypesTest {
    static int passed = 0;
    static int failed = 0;

    static void check(boolean cond, String msg) {
        if (cond) passed++;
        else { failed++; System.out.println("FAIL: " + msg); }
    }

    public static void main(String[] args) throws Exception {
        testManifestValidation();
        testManifestRejectsBadApiVersion();
        testReferenceToString();
        testTensorFromFloat32();
        testTensorRejectsBadFormat();
        testValidation();
        testStoreRoundTrip();
        testStoreRejectsDigestMismatch();

        System.out.println("\nPassed: " + passed + ", Failed: " + failed);
        if (failed > 0) System.exit(1);
    }

    static void testManifestValidation() throws Exception {
        MlManifest manifest = new MlManifest(
                MlContract.MANIFEST_API_VERSION, "example-model", "v1.2.3",
                "nnapi", List.of("image.classify"),
                List.of(new MlArtifact("model", "model.tflite",
                        MlDigest.sha256("model bytes".getBytes()))));
        manifest.validate();
        check(true, "manifest validation");
    }

    static void testManifestRejectsBadApiVersion() throws Exception {
        MlManifest manifest = new MlManifest(
                "content.model/v2", "example-model", "v1.2.3",
                "nnapi", List.of("image.classify"),
                List.of(new MlArtifact("model", "model.tflite",
                        MlDigest.sha256("x".getBytes()))));
        try {
            manifest.validate();
            check(false, "bad apiVersion should be rejected");
        } catch (MlException e) {
            check(e.kind == MlException.Kind.INVALID, "bad apiVersion kind");
        }
    }

    static void testReferenceToString() {
        MlReference ref = new MlReference("nnapi", "example-model", "v1.2.3");
        check("nnapi/example-model@v1.2.3".equals(ref.toString()), "reference toString");
    }

    static void testTensorFromFloat32() throws Exception {
        MlTensor tensor = MlTensor.fromFloat32(new int[]{1, 3, 2, 2}, new float[]{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12});
        check(12 == tensor.elementCount(), "tensor element count");
        check(48 == tensor.data.length, "tensor data length");
        float[] values = tensor.float32Values();
        check(12 == values.length, "tensor values length");
        check(1.0f == values[0], "tensor value 0");
        check(12.0f == values[11], "tensor value 11");
    }

    static void testTensorRejectsBadFormat() {
        try {
            MlTensor bad = new MlTensor(new int[]{2}, new byte[]{1, 2, 3}, "float64");
            bad.validate();
            check(false, "bad format should be rejected");
        } catch (MlException e) {
            check(e.kind == MlException.Kind.INVALID, "bad format kind");
        }
    }

    static void testValidation() throws Exception {
        check(MlValidation.isValidName("example-model", "model"), "valid name");
        check(!MlValidation.isValidName("", "model"), "empty name rejected");
        check(!MlValidation.isValidName("/bad", "model"), "slash name rejected");
        check(MlValidation.isValidVersion("v1.2.3"), "valid version");
        String sha = MlDigest.sha256("test".getBytes());
        check(MlValidation.isHexSha256(sha), "hex sha256");
        check(!MlValidation.isHexSha256("not-hex"), "non-hex rejected");
    }

    static void testStoreRoundTrip() throws Exception {
        Path root = Paths.get(System.getProperty("java.io.tmpdir"))
                .resolve("ml-store-test-" + System.nanoTime());
        java.nio.file.Files.createDirectories(root);
        try {
            MlStore store = new MlStore(root);
            MlManifest manifest = new MlManifest(
                    MlContract.MANIFEST_API_VERSION, "example-model", "v1.2.3",
                    "nnapi", List.of("image.classify"),
                    List.of(new MlArtifact("model", "model.tflite",
                            MlDigest.sha256("model bytes".getBytes()))));
            Map<String, byte[]> files = Map.of("model.tflite", "model bytes".getBytes());

            MlInstalledModel installed = store.install(manifest, files);
            check("example-model".equals(installed.manifest.name), "installed name");

            MlReference ref = new MlReference("nnapi", "example-model", "v1.2.3");
            MlInstalledModel resolved = store.resolve(ref);
            check(installed.directory.equals(resolved.directory), "resolved directory");

            List<MlInstalledModel> list = store.list();
            check(1 == list.size(), "list size");
            check("example-model".equals(list.get(0).manifest.name), "list name");

            store.remove(ref);
            try {
                store.resolve(ref);
                check(false, "removed model should not be found");
            } catch (MlException e) {
                check(e.kind == MlException.Kind.NOT_FOUND, "not found kind");
            }
            check(store.list().isEmpty(), "list empty after remove");
        } finally {
            java.nio.file.Files.walk(root)
                    .sorted(java.util.Collections.reverseOrder())
                    .forEach(p -> { try { java.nio.file.Files.delete(p); } catch (Exception ignored) {} });
        }
    }

    static void testStoreRejectsDigestMismatch() throws Exception {
        Path root = Paths.get(System.getProperty("java.io.tmpdir"))
                .resolve("ml-store-test-" + System.nanoTime());
        java.nio.file.Files.createDirectories(root);
        try {
            MlStore store = new MlStore(root);
            MlManifest manifest = new MlManifest(
                    MlContract.MANIFEST_API_VERSION, "example-model", "v1.2.3",
                    "nnapi", List.of(),
                    List.of(new MlArtifact("model", "model.tflite",
                            MlDigest.sha256("model".getBytes()))));
            try {
                store.install(manifest, Map.of("model.tflite", "tampered".getBytes()));
                check(false, "digest mismatch should be rejected");
            } catch (MlException e) {
                check(e.kind == MlException.Kind.DIGEST_MISMATCH, "digest mismatch kind");
            }
        } finally {
            java.nio.file.Files.walk(root)
                    .sorted(java.util.Collections.reverseOrder())
                    .forEach(p -> { try { java.nio.file.Files.delete(p); } catch (Exception ignored) {} });
        }
    }
}
