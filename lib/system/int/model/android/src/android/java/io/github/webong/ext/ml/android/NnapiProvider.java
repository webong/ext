package io.github.webong.ext.ml.android;

import io.github.webong.ext.ml.MlException;
import io.github.webong.ext.ml.MlInstalledModel;
import io.github.webong.ext.ml.MlProvider;
import io.github.webong.ext.ml.MlSession;
import io.github.webong.ext.ml.MlTensor;

import java.util.List;

/** NNAPI-backed model provider stub. */
public final class NnapiProvider implements MlProvider {
    @Override
    public String name() { return "nnapi"; }

    @Override
    public List<String> capabilities() {
        return java.util.List.of();
    }

    @Override
    public MlSession load(MlInstalledModel model) throws MlException {
        throw new MlException(MlException.Kind.UNAVAILABLE,
                "NNAPI model loading is not implemented in this stub");
    }

    private static final class NnapiSession implements MlSession {
        @Override
        public List<MlTensor> infer(List<MlTensor> inputs) throws MlException {
            throw new MlException(MlException.Kind.UNAVAILABLE, "NNAPI inference is not implemented");
        }

        @Override
        public void close() throws MlException {}
    }
}
