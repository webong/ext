package io.github.webong.ext.ml;

import java.util.List;

public interface MlSession {
    List<MlTensor> infer(List<MlTensor> inputs) throws MlException;
    void close() throws MlException;
}
