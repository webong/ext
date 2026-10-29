package io.github.webong.ext.plugin;

import io.github.webong.ext.ml.MlInstalledModel;
import io.github.webong.ext.ml.MlProvider;
import io.github.webong.ext.ml.MlReference;
import io.github.webong.ext.ml.MlSession;
import io.github.webong.ext.ml.MlStore;
import io.github.webong.ext.ml.MlTensor;
import io.github.webong.ext.ml.MlValidation;

import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import java.util.Map;
import java.util.function.Predicate;

/**
 * Binds a caller-selected Android ML provider to model.tensor/v1. The app owns
 * the guest identity and authorization; the model root arrives in each call.
 * This binding can serve through {@link JsonLine} or be invoked in process.
 */
public final class ModelTensorGuest {
    private static final int MAX_TENSOR_BYTES = 16 << 20;

    private ModelTensorGuest() {}

    public static Guest create(Identity identity, MlProvider provider, Predicate<Call> authorize)
            throws GuestException {
        if (identity == null || provider == null || authorize == null) {
            throw new IllegalArgumentException("identity, provider, and authorization are required");
        }
        Descriptor descriptor = new Descriptor(identity, List.of(new Contract("model.tensor", "v1",
                List.of(new Operation("infer", "action")))));
        return new Guest(descriptor, call -> {
            if (!authorize.test(call)) throw new RemoteError("denied", "operation denied");
            return infer(call, provider);
        });
    }

    private static byte[] infer(Call call, MlProvider provider) throws Exception {
        Map<String, Object> request;
        try {
            request = Json.object(Json.parse(call.payload()));
            if (request == null) throw new IllegalArgumentException("payload must be an object");
        } catch (RuntimeException e) {
            throw new RemoteError("invalid_input", "invalid tensor inference request");
        }
        String root = Json.string(request.get("root"));
        String reference = Json.string(request.get("reference"));
        if (root == null || reference == null || !Path.of(root).isAbsolute())
            throw new RemoteError("invalid_input", "invalid tensor inference request");
        int slash = reference.indexOf('/');
        int at = reference.indexOf('@');
        if (slash < 1 || at <= slash + 1 || at == reference.length() - 1 ||
                reference.indexOf('/', slash + 1) >= 0 || reference.indexOf('@', at + 1) >= 0)
            throw new RemoteError("invalid_input", "invalid tensor inference request");
        String selected = reference.substring(0, slash);
        String name = reference.substring(slash + 1, at);
        String version = reference.substring(at + 1);
        if (!selected.equals(provider.name()) || !MlValidation.isValidName(name, "model name") ||
                !MlValidation.isValidVersion(version))
            throw new RemoteError("invalid_input", "invalid tensor inference request");
        if (!(request.get("inputs") instanceof List<?> raw) || raw.isEmpty() || raw.size() > 64)
            throw new RemoteError("invalid_input", "invalid tensor inference request");
        List<MlTensor> inputs = new ArrayList<>(raw.size());
        int total = 0;
        try {
            for (Object value : raw) {
                Map<String, Object> item = Json.object(value);
                if (item == null || !(item.get("shape") instanceof List<?> shapeRaw) ||
                        !"float32".equals(Json.string(item.get("format"))))
                    throw new IllegalArgumentException("invalid tensor");
                int[] shape = new int[shapeRaw.size()];
                for (int i = 0; i < shape.length; i++) {
                    if (!(shapeRaw.get(i) instanceof Json.Num n))
                        throw new IllegalArgumentException("invalid shape");
                    shape[i] = Integer.parseInt(n.text());
                }
                String encoded = Json.string(item.get("data"));
                if (encoded == null) throw new IllegalArgumentException("missing data");
                byte[] data = Base64.getDecoder().decode(encoded);
                if (data.length > MAX_TENSOR_BYTES - total)
                    throw new IllegalArgumentException("tensor wire limit");
                total += data.length;
                MlTensor tensor = new MlTensor(shape, data, "float32");
                tensor.validate();
                inputs.add(tensor);
            }
        } catch (RuntimeException | io.github.webong.ext.ml.MlException e) {
            throw new RemoteError("invalid_input", "invalid tensor inference request");
        }
        call.checkDeadline();
        MlStore store = new MlStore(Path.of(root));
        MlInstalledModel model = store.resolve(new MlReference(selected, name, version));
        MlSession session = provider.load(model);
        List<MlTensor> outputs;
        try {
            outputs = session.infer(inputs);
        } finally {
            session.close();
        }
        call.checkDeadline();
        if (outputs == null || outputs.isEmpty() || outputs.size() > 64)
            throw new IllegalStateException("provider returned invalid output count");
        total = 0;
        StringBuilder result = new StringBuilder("{\"outputs\":[");
        for (int i = 0; i < outputs.size(); i++) {
            MlTensor tensor = outputs.get(i);
            tensor.validate();
            if (tensor.data.length > MAX_TENSOR_BYTES - total)
                throw new IllegalStateException("provider exceeded tensor wire limit");
            total += tensor.data.length;
            if (i > 0) result.append(',');
            result.append("{\"shape\":[");
            for (int j = 0; j < tensor.shape.length; j++) {
                if (j > 0) result.append(',');
                result.append(tensor.shape[j]);
            }
            result.append("],\"format\":\"float32\",\"data\":");
            Json.quote(result, Base64.getEncoder().encodeToString(tensor.data)).append('}');
        }
        return result.append("]}").toString().getBytes(StandardCharsets.UTF_8);
    }
}
