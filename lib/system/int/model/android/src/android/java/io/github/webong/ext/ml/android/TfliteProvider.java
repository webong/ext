package io.github.webong.ext.ml.android;

import io.github.webong.ext.ml.MlArtifact;
import io.github.webong.ext.ml.MlContract;
import io.github.webong.ext.ml.MlDigest;
import io.github.webong.ext.ml.MlException;
import io.github.webong.ext.ml.MlInstalledModel;
import io.github.webong.ext.ml.MlProvider;
import io.github.webong.ext.ml.MlSession;
import io.github.webong.ext.ml.MlTensor;

import java.io.IOException;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** Optional TensorFlow Lite backend for verified float32 model artifacts. */
public final class TfliteProvider implements MlProvider {
    private final int maxInputs;
    private final int maxOutputs;
    private final int maxTensorBytes;

    public TfliteProvider() { this(16, 16, 64 * 1024 * 1024); }

    public TfliteProvider(int maxInputs, int maxOutputs, int maxTensorBytes) {
        if (maxInputs < 1 || maxInputs > 256 || maxOutputs < 1 || maxOutputs > 256 || maxTensorBytes < 1)
            throw new IllegalArgumentException("invalid TensorFlow Lite limits");
        this.maxInputs = maxInputs;
        this.maxOutputs = maxOutputs;
        this.maxTensorBytes = maxTensorBytes;
    }

    @Override public String name() { return "tflite"; }
    @Override public List<String> capabilities() { return List.of("tensor_float32"); }

    @Override public MlSession load(MlInstalledModel model) throws MlException {
        if (model == null || model.manifest == null || model.directory == null)
            throw new MlException(MlException.Kind.INVALID, "model is required");
        model.manifest.validate();
        MlArtifact artifact = null;
        for (MlArtifact candidate : model.manifest.artifacts) {
            if ("model".equals(candidate.role)) { artifact = candidate; break; }
        }
        if (artifact == null || !artifact.file.endsWith(".tflite"))
            throw new MlException(MlException.Kind.INVALID, "a .tflite model artifact is required");
        Path path = Path.of(model.directory).resolve(artifact.file);
        try {
            if (Files.isSymbolicLink(path) || !Files.isRegularFile(path))
                throw new MlException(MlException.Kind.INVALID, "model artifact must be a regular file");
            long size = Files.size(path);
            if (size < 1 || size > MlContract.MAX_ARTIFACT_BYTES)
                throw new MlException(MlException.Kind.INVALID, "model artifact exceeds size limit");
            byte[] bytes = Files.readAllBytes(path);
            if (!MlDigest.sha256(bytes).equals(artifact.sha256))
                throw new MlException(MlException.Kind.DIGEST_MISMATCH, "model artifact digest changed");
            ByteBuffer buffer = ByteBuffer.allocateDirect(bytes.length).order(ByteOrder.nativeOrder());
            buffer.put(bytes).rewind();
            Class<?> interpreterClass;
            try { interpreterClass = Class.forName("org.tensorflow.lite.Interpreter"); }
            catch (ClassNotFoundException e) {
                throw new MlException(MlException.Kind.UNAVAILABLE, "TensorFlow Lite runtime is not installed", e);
            }
            Object interpreter = interpreterClass.getConstructor(ByteBuffer.class).newInstance(buffer);
            return new Session(interpreter, interpreterClass, buffer, maxInputs, maxOutputs, maxTensorBytes);
        } catch (IOException e) {
            throw new MlException(MlException.Kind.NOT_FOUND, "read model artifact", e);
        } catch (ReflectiveOperationException e) {
            throw new MlException(MlException.Kind.NATIVE_FAILURE, "open TensorFlow Lite model", e);
        }
    }

    private static final class Session implements MlSession {
        private final Object interpreter;
        private final Class<?> type;
        @SuppressWarnings("unused") private final ByteBuffer modelBuffer;
        private final int maxInputs, maxOutputs, maxTensorBytes;
        private boolean closed;

        Session(Object interpreter, Class<?> type, ByteBuffer modelBuffer, int maxInputs, int maxOutputs, int maxTensorBytes) {
            this.interpreter = interpreter; this.type = type; this.modelBuffer = modelBuffer;
            this.maxInputs = maxInputs; this.maxOutputs = maxOutputs; this.maxTensorBytes = maxTensorBytes;
        }

        @Override public synchronized List<MlTensor> infer(List<MlTensor> inputs) throws MlException {
            if (closed) throw new MlException(MlException.Kind.NOT_LOADED, "TensorFlow Lite session is closed");
            if (inputs == null || inputs.isEmpty() || inputs.size() > maxInputs)
                throw new MlException(MlException.Kind.INVALID, "input tensor count out of range");
            try {
                int inputCount = (Integer) call("getInputTensorCount", new Class<?>[0]);
                int outputCount = (Integer) call("getOutputTensorCount", new Class<?>[0]);
                if (inputs.size() != inputCount || outputCount < 1 || outputCount > maxOutputs)
                    throw new MlException(MlException.Kind.INVALID, "model tensor count out of range");
                Object[] nativeInputs = new Object[inputCount];
                for (int i = 0; i < inputCount; i++) {
                    MlTensor input = inputs.get(i);
                    input.validate();
                    if (input.data.length > maxTensorBytes)
                        throw new MlException(MlException.Kind.INVALID, "input tensor exceeds size limit");
                    Object tensor = call("getInputTensor", new Class<?>[]{int.class}, i);
                    if (!"FLOAT32".equals(tensor.getClass().getMethod("dataType").invoke(tensor).toString()) ||
                            !Arrays.equals(input.shape, (int[]) tensor.getClass().getMethod("shape").invoke(tensor)))
                        throw new MlException(MlException.Kind.INVALID, "input tensor shape or type mismatch");
                    ByteBuffer buffer = ByteBuffer.allocateDirect(input.data.length).order(ByteOrder.nativeOrder());
                    buffer.put(input.data).rewind();
                    nativeInputs[i] = buffer;
                }
                Map<Integer, Object> nativeOutputs = new HashMap<>();
                int[][] shapes = new int[outputCount][];
                for (int i = 0; i < outputCount; i++) {
                    Object tensor = call("getOutputTensor", new Class<?>[]{int.class}, i);
                    if (!"FLOAT32".equals(tensor.getClass().getMethod("dataType").invoke(tensor).toString()))
                        throw new MlException(MlException.Kind.INVALID, "only float32 outputs are supported");
                    int[] shape = (int[]) tensor.getClass().getMethod("shape").invoke(tensor);
                    long elements = 1;
                    if (shape.length < 1 || shape.length > 8) throw new MlException(MlException.Kind.INVALID, "output shape invalid");
                    for (int dim : shape) {
                        if (dim < 1 || elements > maxTensorBytes / 4L / dim)
                            throw new MlException(MlException.Kind.INVALID, "output tensor exceeds size limit");
                        elements *= dim;
                    }
                    shapes[i] = shape;
                    nativeOutputs.put(i, ByteBuffer.allocateDirect((int) elements * 4).order(ByteOrder.nativeOrder()));
                }
                call("runForMultipleInputsOutputs", new Class<?>[]{Object[].class, Map.class}, nativeInputs, nativeOutputs);
                List<MlTensor> outputs = new ArrayList<>(outputCount);
                for (int i = 0; i < outputCount; i++) {
                    ByteBuffer buffer = (ByteBuffer) nativeOutputs.get(i);
                    byte[] data = new byte[buffer.capacity()];
                    buffer.rewind(); buffer.get(data);
                    MlTensor output = new MlTensor(shapes[i], data, "float32");
                    output.validate();
                    outputs.add(output);
                }
                return outputs;
            } catch (ReflectiveOperationException e) {
                throw new MlException(MlException.Kind.NATIVE_FAILURE, "TensorFlow Lite inference failed", e);
            }
        }

        private Object call(String method, Class<?>[] signature, Object... args) throws ReflectiveOperationException {
            Method target = type.getMethod(method, signature);
            try { return target.invoke(interpreter, args); }
            catch (InvocationTargetException e) { throw e; }
        }

        @Override public synchronized void close() throws MlException {
            if (closed) return;
            closed = true;
            try { call("close", new Class<?>[0]); }
            catch (ReflectiveOperationException e) {
                throw new MlException(MlException.Kind.NATIVE_FAILURE, "close TensorFlow Lite model", e);
            }
        }
    }
}
