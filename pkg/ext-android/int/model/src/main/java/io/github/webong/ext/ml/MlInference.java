package io.github.webong.ext.ml;

import java.util.List;

public final class MlInference {
    private MlInference() {}

    public static List<MlTensor> run(MlProvider provider, MlInstalledModel model,
                                     List<MlTensor> inputs) throws MlException {
        MlSession session = provider.load(model);
        try {
            return session.infer(inputs);
        } finally {
            try { session.close(); } catch (MlException ignored) {}
        }
    }
}
