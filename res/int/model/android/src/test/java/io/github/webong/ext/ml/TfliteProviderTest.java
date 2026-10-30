package io.github.webong.ext.ml;

import io.github.webong.ext.ml.android.TfliteProvider;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

public final class TfliteProviderTest {
    private TfliteProviderTest() {}

    public static void main(String[] args) throws Exception {
        Path dir = Files.createTempDirectory("ext-tflite-test-");
        try {
            byte[] modelBytes = new byte[]{1, 2, 3};
            Files.write(dir.resolve("model.tflite"), modelBytes);
            MlManifest manifest = new MlManifest(MlContract.MANIFEST_API_VERSION, "sample", "1", "tflite",
                    List.of(), List.of(new MlArtifact("model", "model.tflite", MlDigest.sha256(modelBytes))));
            MlInstalledModel model = new MlInstalledModel(manifest, dir.toString());
            TfliteProvider provider = new TfliteProvider();
            MlSession session = provider.load(model);
            List<MlTensor> outputs = session.infer(List.of(MlTensor.fromFloat32(new int[]{1, 2}, new float[]{2, 3})));
            float[] values = outputs.get(0).float32Values();
            if (values.length != 2 || values[0] != 4 || values[1] != 6)
                throw new AssertionError("wrong output tensor");
            session.close();
            try {
                session.infer(List.of(MlTensor.fromFloat32(new int[]{1, 2}, new float[]{2, 3})));
                throw new AssertionError("closed session accepted inference");
            } catch (MlException e) {
                if (e.kind != MlException.Kind.NOT_LOADED) throw e;
            }
            Files.write(dir.resolve("model.tflite"), new byte[]{9});
            try {
                provider.load(model);
                throw new AssertionError("changed model accepted");
            } catch (MlException e) {
                if (e.kind != MlException.Kind.DIGEST_MISMATCH) throw e;
            }
            System.out.println("TensorFlow Lite provider test passed");
        } finally {
            Files.deleteIfExists(dir.resolve("model.tflite"));
            Files.deleteIfExists(dir);
        }
    }
}
