package io.github.webong.ext.plugin;

import io.github.webong.ext.ml.MlArtifact;
import io.github.webong.ext.ml.MlContract;
import io.github.webong.ext.ml.MlDigest;
import io.github.webong.ext.ml.MlManifest;
import io.github.webong.ext.ml.MlStore;
import io.github.webong.ext.ml.android.TfliteProvider;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Base64;
import java.util.Comparator;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;

/** Plain Java test of the optional TFLite backend through model.tensor/v1. */
public final class ModelTensorGuestTests {
    private ModelTensorGuestTests() {}

    public static void main(String[] args) throws Exception {
        Path root = Files.createTempDirectory("ext-tensor-guest-");
        try {
            byte[] model = new byte[]{1, 2, 3};
            MlStore store = new MlStore(root);
            MlManifest manifest = new MlManifest(MlContract.MANIFEST_API_VERSION, "sample", "1", "tflite",
                    List.of(), List.of(new MlArtifact("model", "model.tflite", MlDigest.sha256(model))));
            store.installFromBytes(manifest, Map.of("model.tflite", model));
            byte[] inputs = new byte[]{0, 0, 0, 64, 0, 0, 64, 64};
            StringBuilder request = new StringBuilder("{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":")
                    .append("{\"id\":\"tflite\",\"revision\":\"r1\",\"version\":\"1.0.0\"},")
                    .append("\"contract\":{\"name\":\"model.tensor\",\"version\":\"v1\"},")
                    .append("\"operation\":\"infer\",\"surface\":\"action\",\"deadline\":\"2099-01-01T00:00:00Z\",")
                    .append("\"payload\":{\"root\":");
            Json.quote(request, root.toString()).append(",\"reference\":\"tflite/sample@1\",\"inputs\":[")
                    .append("{\"shape\":[1,2],\"format\":\"float32\",\"data\":");
            Json.quote(request, Base64.getEncoder().encodeToString(inputs)).append("}]}}");
            Identity identity = new Identity("tflite", "r1", "1.0.0");
            try (Guest guest = ModelTensorGuest.create(identity, new TfliteProvider(), call -> true)) {
                byte[] response = guest.invoke(request.toString().getBytes(StandardCharsets.UTF_8));
                Map<String, Object> envelope = Json.object(Json.parse(response));
                Map<String, Object> payload = Json.object(envelope.get("payload"));
                if (payload == null || !(payload.get("outputs") instanceof List<?> outputs) || outputs.size() != 1)
                    throw new AssertionError("missing output: " + new String(response, StandardCharsets.UTF_8));
                Map<String, Object> output = Json.object(outputs.get(0));
                byte[] bytes = Base64.getDecoder().decode(Json.string(output.get("data")));
                if (!java.util.Arrays.equals(bytes, new byte[]{0, 0, (byte) 128, 64, 0, 0, (byte) 192, 64}))
                    throw new AssertionError("wrong TFLite output");
            }
            try (Guest denied = ModelTensorGuest.create(identity, new TfliteProvider(), call -> false)) {
                String response = new String(denied.invoke(request.toString().getBytes(StandardCharsets.UTF_8)),
                        StandardCharsets.UTF_8);
                if (!response.contains("\"code\":\"denied\""))
                    throw new AssertionError("denied call was accepted: " + response);
            }
            System.out.println("Android tensor guest test passed");
        } finally {
            try (Stream<Path> paths = Files.walk(root)) {
                for (Path path : paths.sorted(Comparator.reverseOrder()).toList()) Files.deleteIfExists(path);
            }
        }
    }
}
